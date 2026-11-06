package api

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
	phoenixaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	phoenixlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
)

// PhoenixOrderBackend extends the read-only account backend with bounded local
// construction and fresh chain evidence. It still exposes no order submission.
type PhoenixOrderBackend interface {
	PhoenixAccountBackend
	phoenixlive.Backend
	Address() string
	BuilderAuthority() string
	LatestBlockhash(context.Context) (rpc.Blockhash, error)
	BlockHeight(context.Context, uint64) (uint64, error)
	FlightProof(context.Context, uint64) (phoenixlive.FlightProof, error)
}

type phoenixOrderPreparer struct {
	deps    *LiveDeps
	config  PhoenixAccountConfig
	backend PhoenixOrderBackend
}

func phoenixOrderMarket(snapshot phoenix.MarketStreamSnapshot, symbol string) (phoenix.MarketMetadata, helper.MarketAccounts, error) {
	if !snapshot.Connected || !snapshot.Synchronized || snapshot.Generation == 0 || snapshot.MetadataGeneration == 0 {
		return phoenix.MarketMetadata{}, helper.MarketAccounts{}, errors.New("Phoenix order market stream is not synchronized")
	}
	for _, market := range snapshot.Metadata.Markets {
		if market.Symbol != symbol {
			continue
		}
		if market.Status != "active" || market.BaseLotDecimals < -18 || market.BaseLotDecimals > 18 || market.TickSize == 0 ||
			market.AmountIncrement != math.Pow10(-int(market.BaseLotDecimals)) || market.PriceIncrement != float64(market.TickSize)*math.Pow10(int(market.BaseLotDecimals))/1_000_000 {
			return phoenix.MarketMetadata{}, helper.MarketAccounts{}, errors.New("Phoenix IOC market precision or status unavailable")
		}
		keys := snapshot.Metadata.Accounts
		accounts := helper.MarketAccounts{PerpAssetMap: keys.PerpAssetMap, Orderbook: market.MarketPubkey, SplineCollection: market.SplinePubkey,
			GlobalTraderIndex: append([]string(nil), keys.GlobalTraderIndex...), ActiveTraderBuffer: append([]string(nil), keys.ActiveTraderBuffer...)}
		if len(accounts.GlobalTraderIndex) == 0 || len(accounts.GlobalTraderIndex) > 64 || len(accounts.ActiveTraderBuffer) == 0 || len(accounts.ActiveTraderBuffer) > 64 {
			return phoenix.MarketMetadata{}, helper.MarketAccounts{}, errors.New("Phoenix order account lists unavailable")
		}
		for _, key := range append(append([]string{accounts.PerpAssetMap, accounts.Orderbook, accounts.SplineCollection}, accounts.GlobalTraderIndex...), accounts.ActiveTraderBuffer...) {
			if !phoenixaccount.ValidOwner(key) {
				return phoenix.MarketMetadata{}, helper.MarketAccounts{}, errors.New("invalid Phoenix order account")
			}
		}
		return market, accounts, nil
	}
	return phoenix.MarketMetadata{}, helper.MarketAccounts{}, errors.New("Phoenix order market unavailable")
}

// NormalizeHedgeAmount only reads cached rules. SDK normalization is local and
// must not depend on an owner's collateral catalog or a risk/Flight projection.
func (p *phoenixOrderPreparer) NormalizeAmount(ctx context.Context, symbol string, amount float64) (float64, error) {
	before := p.config.Markets.Snapshot()
	market, _, err := phoenixOrderMarket(before, symbol)
	if err != nil {
		return 0, err
	}
	q := helper.NormalizeRequest{Symbol: symbol, Side: helper.SideBid, Amount: strconv.FormatFloat(amount, 'f', -1, 64),
		LimitPrice: strconv.FormatFloat(2*market.PriceIncrement, 'f', -1, 64), BaseLotDecimals: int8(market.BaseLotDecimals),
		TickSize: strconv.FormatUint(market.TickSize, 10), MetadataGeneration: before.MetadataGeneration}
	r, err := p.backend.NormalizeOrder(ctx, q)
	if err != nil {
		return 0, err
	}
	if err := helper.ValidateNormalizedOrder(q, r); err != nil {
		return 0, err
	}
	after := p.config.Markets.Snapshot()
	if !after.Connected || !after.Synchronized || after.Generation != before.Generation || after.Sequence != before.Sequence || after.MetadataGeneration != before.MetadataGeneration {
		return 0, errors.New("Phoenix amount rules changed during normalization")
	}
	v, err := strconv.ParseFloat(r.Amount, 64)
	if err != nil || !positiveLiveNumber(v) || v > amount {
		return 0, errors.New("invalid Phoenix normalized amount")
	}
	return v, nil
}

func positiveLiveNumber(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func phoenixOrderCapability(state phoenixaccount.Snapshot, action helper.Action) bool {
	c := state.Cross.Capabilities
	if (c.State != "active" && c.State != "cold" && c.State != "reduceOnly") || !phoenixAccess(c.Actions.PlaceMarketOrder) {
		return false
	}
	if action == helper.ActionOpen {
		return phoenixAccess(c.Actions.RiskIncreasingTrade)
	}
	return phoenixAccess(c.Actions.RiskReducingTrade)
}

func phoenixOrderQuote(snapshot phoenix.MarketStreamSnapshot, symbol, side string) (float64, time.Time, error) {
	book, ok := snapshot.Books[symbol]
	if !ok || book.Symbol != symbol || book.Generation != snapshot.Generation || !phoenixTimeFresh(book.ReceivedAt, time.Now(), helper.ProjectionQuoteMaxAge) {
		return 0, time.Time{}, errors.New("Phoenix execution BBO is stale or unavailable")
	}
	price, size := book.AskPrice, book.AskSizeUSD
	if side == "sell" {
		price, size = book.BidPrice, book.BidSizeUSD
	}
	if !positiveLiveNumber(price) || !positiveLiveNumber(size) {
		return 0, time.Time{}, errors.New("Phoenix execution BBO side unavailable")
	}
	return price, book.ReceivedAt, nil
}

func (p *phoenixOrderPreparer) Prepare(ctx context.Context, q phoenixlive.Intent) (*domain.SigningRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	acquire := p.deps.accounts.Acquire
	if q.Action != helper.ActionOpen {
		acquire = p.deps.accounts.AcquireRecovery
	}
	lease, err := acquire("phoenix", q.Owner)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	feed, ok := lease.Feed().(*phoenixAccountFeed)
	if !ok {
		return nil, errors.New("Phoenix order account feed unavailable")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(feed.ctx, cancel)
	defer func() { stop(); cancel() }()
	state := feed.state.Snapshot()
	if q.Action == helper.ActionOpen && feed.factory.orderJournal != nil {
		pending, err := feed.factory.orderJournal.Unresolved(ctx, q.Owner)
		if err != nil {
			return nil, err
		}
		if pending {
			return nil, errors.New("Phoenix prior order outcome requires reconciliation")
		}
	}
	if !state.Fresh(time.Now()) || !phoenixOrderCapability(state, q.Action) {
		return nil, errors.New("Phoenix fresh account trading capability required")
	}
	feed.mu.Lock()
	revision := feed.authorityRevision
	feed.mu.Unlock()
	// Warm complete risk parameters before taking the 5s execution quote. Safety
	// actions never read collateral parameters or invoke the opening risk model.
	if q.Action == helper.ActionOpen {
		if err := feed.factory.risk.RefreshParameters(ctx); err != nil {
			return nil, err
		}
	}
	blockhashAt := time.Now().UTC()
	hash, err := p.backend.LatestBlockhash(ctx)
	if err != nil {
		return nil, err
	}
	height, err := p.backend.BlockHeight(ctx, hash.Slot)
	if err != nil {
		return nil, err
	}
	authorityAt := time.Now().UTC()
	authority, err := p.backend.AccountAuthority(ctx, q.Owner, max(state.Slot, hash.Slot))
	if err != nil {
		return nil, err
	}
	if authority.PositionAuthority != q.Agent {
		return nil, errors.New("Phoenix on-chain agent differs from intent")
	}
	authorized, err := feed.factory.store.AgentAuthorizationMatches(ctx, "phoenix", q.Owner, q.Agent)
	if err != nil || !authorized {
		return nil, errors.New("Phoenix registered agent does not match intent")
	}
	var flight *phoenixlive.FlightProof
	if q.Action == helper.ActionOpen || q.Action == helper.ActionClose {
		proof, err := p.backend.FlightProof(ctx, hash.Slot)
		if err != nil {
			return nil, err
		}
		flight = &proof
	}
	// Chain reads may overlap ordinary account updates. Capture the latest state
	// now, provided the verified authority bank covers it, then fence it through
	// normalization/projection/build instead of retaining a pre-RPC snapshot.
	state = feed.state.Snapshot()
	if !state.Fresh(time.Now()) || authority.Slot < state.Slot || !phoenixOrderCapability(state, q.Action) {
		return nil, errors.New("Phoenix authority does not cover current account state")
	}
	stream := p.config.Markets.Snapshot()
	market, accounts, err := phoenixOrderMarket(stream, q.Symbol)
	if err != nil {
		return nil, err
	}
	price, quoteAt, err := phoenixOrderQuote(stream, q.Symbol, q.Side)
	if err != nil {
		return nil, err
	}
	q.QuoteObservedAt = quoteAt
	if q.Action != helper.ActionOpen {
		q.ReferencePrice = price
		q.LimitPrice, err = phoenixlive.ReductionLimit(price, q.Side)
		if err != nil {
			return nil, err
		}
	}
	capture := phoenixlive.Capture{Symbol: q.Symbol, MetadataGeneration: stream.MetadataGeneration,
		StreamGeneration: stream.Generation, ExchangeSequence: stream.Sequence, MarketMetadataGeneration: stream.MetadataGeneration,
		MetadataObservedAt: quoteAt, BaseLotDecimals: int8(market.BaseLotDecimals), TickSize: strconv.FormatUint(market.TickSize, 10), Accounts: accounts,
		AccountGeneration: state.Generation, AccountSlot: state.Slot, AccountSequence: state.Sequence, AccountObservedAt: state.ObservedAt,
		Authority: phoenixlive.AuthorityProof{Owner: q.Owner, Agent: q.Agent, RegisteredAgent: q.Agent, ObservedAt: authorityAt, Slot: authority.Slot,
			CanIncreaseRisk: phoenixOrderCapability(state, helper.ActionOpen), CanReduceRisk: phoenixOrderCapability(state, helper.ActionUnwind)},
		Blockhash: hash, BlockHeight: height, BlockhashObservedAt: blockhashAt, Flight: flight}
	check := func(ctx context.Context, intent phoenixlive.Intent, captured phoenixlive.Capture) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := feed.state.Snapshot()
		if feed.ctx.Err() != nil || !current.Fresh(time.Now()) || current.Generation != captured.AccountGeneration || current.Slot != captured.AccountSlot || current.Sequence != captured.AccountSequence || !phoenixOrderCapability(current, intent.Action) {
			return errors.New("Phoenix account changed during order preparation")
		}
		feed.mu.Lock()
		changed := feed.authorityRevision != revision
		feed.mu.Unlock()
		if changed {
			return errors.New("Phoenix authority changed during order preparation")
		}
		matches, err := feed.factory.store.AgentAuthorizationMatches(ctx, "phoenix", intent.Owner, intent.Agent)
		if err != nil || !matches {
			return errors.New("Phoenix agent authorization changed during order preparation")
		}
		latest := p.config.Markets.Snapshot()
		latestMarket, latestAccounts, err := phoenixOrderMarket(latest, intent.Symbol)
		if err != nil || latest.Generation != captured.StreamGeneration || latest.Sequence != captured.ExchangeSequence || latest.MetadataGeneration != captured.MarketMetadataGeneration || latestMarket != market || !reflect.DeepEqual(latestAccounts, captured.Accounts) {
			return errors.New("Phoenix order market metadata changed during preparation")
		}
		latestPrice, _, err := phoenixOrderQuote(latest, intent.Symbol, intent.Side)
		if err != nil {
			return err
		}
		if intent.Action == helper.ActionOpen && ((intent.Side == "buy" && latestPrice > intent.LimitPrice) || (intent.Side == "sell" && latestPrice < intent.LimitPrice)) {
			return errors.New("Phoenix execution BBO moved outside approved limit")
		}
		if intent.Action == helper.ActionOpen && captured.Projection != nil {
			if _, err := feed.factory.risk.ParameterObservation(captured.MetadataGeneration, captured.StreamGeneration, captured.ExchangeSequence); err != nil {
				return err
			}
		}
		return nil
	}
	builder, err := phoenixlive.NewBuilder(p.backend, p.backend.Address(), p.backend.BuilderAuthority(), check)
	if err != nil {
		return nil, err
	}
	if q.Action == helper.ActionOpen {
		normalized, err := builder.Normalize(ctx, q, capture)
		if err != nil {
			return nil, err
		}
		projection, err := feed.factory.risk.ProjectOpen(ctx, feed.state, p.backend, helper.OrderProjection{IntentID: q.ID, Symbol: q.Symbol, Side: string(normalized.Side),
			NumBaseLots: strconv.FormatUint(normalized.NumBaseLots, 10), PriceLimitTicks: strconv.FormatUint(normalized.PriceLimitTicks, 10), QuoteObservedUnixMS: quoteAt.UnixMilli(), FlightFeeBPS: 2})
		if err != nil {
			return nil, err
		}
		observed, err := feed.factory.risk.ParameterObservation(projection.MetadataGeneration, stream.Generation, stream.Sequence)
		if err != nil {
			return nil, err
		}
		capture.Projection, capture.MetadataGeneration, capture.MetadataObservedAt = &projection, projection.MetadataGeneration, observed
	}
	request, err := builder.Prepare(ctx, q, capture)
	if err != nil {
		return nil, err
	}
	// Tick normalization can tighten the approved bound. Recheck executability
	// against that actual wire limit rather than the pre-normalization float.
	bounded := q
	bounded.LimitPrice = request.LimitPrice
	if err := check(ctx, bounded, capture); err != nil {
		return nil, err
	}
	if !time.Now().Before(request.ExpiresAt) {
		return nil, errors.New("Phoenix order expired during preparation")
	}
	return request, nil
}
