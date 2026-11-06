package api

import (
	"context"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"
)

type PhoenixSubmitBackend interface {
	PhoenixOrderBackend
	orders.Backend
}

func (f *phoenixAccountFeed) SubmitSigned(ctx context.Context, signed domain.SignedAction, q *domain.SigningRequest) (*domain.SubmissionResult, error) {
	if f.factory.orders == nil {
		return nil, errPhoenixLiveDisabled
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(f.ctx, cancel)
	defer func() { stop(); cancel() }()
	return f.factory.orders.Submit(ctx, q, signed, f.validateOrderSubmit)
}

func (f *phoenixAccountFeed) validateOrderSubmit(ctx context.Context, q *domain.SigningRequest, p orders.Prepared) (orders.Admission, error) {
	failed := func(err error) (orders.Admission, error) { return orders.Admission{}, err }
	b, ok := f.factory.config.Backend.(PhoenixSubmitBackend)
	if !ok || q.Account != f.state.Snapshot().Owner {
		return failed(orders.ErrConflict)
	}
	var projectionGeneration uint64
	var projectionValidUntil int64
	if q.Action == "open" {
		pending, err := f.factory.orderJournal.Unresolved(ctx, q.Account)
		if err != nil {
			return failed(err)
		}
		if pending {
			return failed(orders.ErrUncertain)
		}
		if err := f.factory.risk.RefreshParameters(ctx); err != nil {
			return failed(err)
		}
	}
	if err := f.subscriber.Refresh(ctx); err != nil {
		return failed(err)
	}
	before := f.state.Snapshot()
	if !before.Fresh(time.Now()) || !phoenixOrderCapability(before, helper.Action(q.Action)) {
		return failed(errors.New("Phoenix current account capability unavailable"))
	}
	f.mu.Lock()
	revision := f.authorityRevision
	f.mu.Unlock()
	hash, err := b.LatestBlockhash(ctx)
	if err != nil {
		return failed(err)
	}
	if hash.Slot >= p.LastSlot {
		return failed(errors.New("Phoenix signed native slot expired"))
	}
	authority, err := b.AccountAuthority(ctx, q.Account, max(before.Slot, hash.Slot, p.Metadata.MinContextSlot))
	if err != nil {
		return failed(err)
	}
	if authority.PositionAuthority != q.Signer || authority.Slot < max(before.Slot, hash.Slot, p.Metadata.MinContextSlot) || authority.Slot >= p.LastSlot {
		return failed(errors.New("Phoenix current agent/slot differs from signed order"))
	}
	if q.Action == "open" || q.Action == "close" {
		proof, err := b.FlightProof(ctx, hash.Slot)
		if err != nil {
			return failed(err)
		}
		flight := p.Metadata.Built.Manifest.Flight
		if flight == nil || proof.ProgramOwner != helper.FlightProgramID || proof.FeeBPS != 2 || proof.BuilderAuthority != flight.BuilderAuthority || proof.BuilderState != flight.BuilderState || proof.BuilderTraderAccount != flight.BuilderTraderAccount || proof.Slot >= p.LastSlot || proof.Slot < hash.Slot || !phoenixTimeFresh(proof.ObservedAt, time.Now(), admissionFreshness) {
			return failed(errors.New("Phoenix Flight registration differs from signed order"))
		}
	}
	before = f.state.Snapshot()
	if !before.Fresh(time.Now()) || authority.Slot < before.Slot || !phoenixOrderCapability(before, helper.Action(q.Action)) {
		return failed(errors.New("Phoenix authority does not cover current order account"))
	}
	stream := f.factory.config.Markets.Snapshot()
	market, accounts, err := phoenixOrderMarket(stream, q.Symbol)
	if err != nil {
		return failed(err)
	}
	if int8(market.BaseLotDecimals) != p.Metadata.Normalized.BaseLotDecimals || strconv.FormatUint(market.TickSize, 10) != p.Metadata.Normalized.TickSize {
		return failed(errors.New("Phoenix signed order precision changed"))
	}
	price, quoteAt, err := phoenixOrderQuote(stream, q.Symbol, q.Side)
	if err != nil {
		return failed(err)
	}
	if (q.Side == "buy" && price > q.LimitPrice) || (q.Side == "sell" && price < q.LimitPrice) {
		return failed(errors.New("Phoenix BBO outside signed limit"))
	}
	request := helper.BuildOrderRequest{Action: helper.Action(q.Action), OwnerAuthority: q.Account, PositionAuthority: q.Signer, FeePayer: b.Address(), Symbol: q.Symbol,
		Side: p.Metadata.Normalized.Side, NumBaseLots: p.Lots, PriceLimitTicks: p.Ticks, ReduceOnly: q.ReduceOnly,
		ClientOrderID: p.Metadata.Built.Manifest.ClientOrderID, LastValidSlot: p.LastSlot, RecentBlockhash: p.Metadata.Built.RecentBlockhash,
		LastValidBlockHeight: p.Metadata.Built.LastValidBlockHeight, MetadataGeneration: p.Metadata.Built.MetadataGeneration, Accounts: accounts}
	if q.Action == "open" || q.Action == "close" {
		request.BuilderAuthority = b.BuilderAuthority()
	}
	built, err := b.BuildOrder(ctx, request)
	if err != nil {
		return failed(err)
	}
	if built.MessageBase64 != p.Metadata.Built.MessageBase64 || built.MessageSHA256 != p.Metadata.Built.MessageSHA256 {
		return failed(errors.New("Phoenix SDK rebuild differs from stored signed bytes"))
	}
	if q.Action == "open" {
		result, err := f.factory.risk.ProjectOpen(ctx, f.state, b, helper.OrderProjection{IntentID: q.ID, Symbol: q.Symbol, Side: string(p.Metadata.Normalized.Side), NumBaseLots: p.Payload.NumBaseLots, PriceLimitTicks: p.Payload.PriceLimitTicks, QuoteObservedUnixMS: quoteAt.UnixMilli(), FlightFeeBPS: 2})
		if err != nil {
			return failed(err)
		}
		if result.Projection == nil || !result.Projection.MarginSufficient || result.RiskTier != "Safe" || result.Projection.ProjectedRiskTier != "Safe" || result.AccountGeneration != before.Generation || result.AccountSlot != before.Slot || result.AccountSequence != before.Sequence {
			return failed(errors.New("Phoenix current opening projection does not admit signed order"))
		}
		projectionGeneration, projectionValidUntil = result.MetadataGeneration, result.ValidUntilUnixMS
	}
	check := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !time.Now().Before(q.ExpiresAt) {
			return errors.New("Phoenix signed order expired")
		}
		if q.Action == "open" {
			if time.Now().UnixMilli() >= projectionValidUntil {
				return errors.New("Phoenix submission projection expired")
			}
			if _, err := f.factory.risk.ParameterObservation(projectionGeneration, stream.Generation, stream.Sequence); err != nil {
				return err
			}
		}
		current := f.state.Snapshot()
		if f.ctx.Err() != nil || !current.Fresh(time.Now()) || current.Generation != before.Generation || current.Slot != before.Slot || current.Sequence != before.Sequence || !phoenixOrderCapability(current, helper.Action(q.Action)) {
			return errors.New("Phoenix account changed before send")
		}
		f.mu.Lock()
		changed := f.authorityRevision != revision
		f.mu.Unlock()
		if changed {
			return errors.New("Phoenix authority changed before send")
		}
		matches, err := f.factory.store.AgentAuthorizationMatches(ctx, "phoenix", q.Account, q.Signer)
		if err != nil || !matches {
			return errors.New("Phoenix agent registration changed before send")
		}
		latest := f.factory.config.Markets.Snapshot()
		latestMarket, latestAccounts, err := phoenixOrderMarket(latest, q.Symbol)
		if err != nil || latest.Generation != stream.Generation || latest.Sequence != stream.Sequence || latest.MetadataGeneration != stream.MetadataGeneration || latestMarket != market || !reflect.DeepEqual(accounts, latestAccounts) {
			return errors.New("Phoenix market changed before send")
		}
		price, _, err := phoenixOrderQuote(latest, q.Symbol, q.Side)
		if err != nil {
			return err
		}
		if (q.Side == "buy" && price > q.LimitPrice) || (q.Side == "sell" && price < q.LimitPrice) {
			return errors.New("Phoenix BBO outside signed limit before send")
		}
		return nil
	}
	if err := check(ctx); err != nil {
		return failed(err)
	}
	base := "0"
	if position, ok := before.Cross.Positions[q.Symbol]; ok {
		base = position.BasePositionLots
	}
	return orders.Admission{Before: orders.Before{BaseLots: base, Generation: before.Generation, Slot: before.Slot, Sequence: before.Sequence, ObservedAt: before.ObservedAt}, MinSlot: max(p.Metadata.MinContextSlot, authority.Slot, hash.Slot), Check: check}, nil
}

func (f *phoenixAccountFeed) WaitForFill(ctx context.Context, q *domain.SigningRequest) (*normFill, error) {
	if f.factory.orders == nil {
		return nil, errPhoenixLiveDisabled
	}
	if q == nil || q.Venue != "phoenix" || q.Account != f.state.Snapshot().Owner {
		return nil, orders.ErrConflict
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(f.ctx, cancel)
	defer func() { stop(); cancel() }()
	a, err := f.factory.orders.Wait(ctx, q.Account, q.ID, func(ctx context.Context, minimum uint64) error {
		if err := f.subscriber.Refresh(ctx); err != nil {
			return err
		}
		state := f.state.Snapshot()
		if !state.Fresh(time.Now()) || state.Slot < minimum {
			return errors.New("Phoenix fill account state is not fresh at transaction slot")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if a.Request.ClientOrderID != q.ClientOrderID || a.Request.Symbol != q.Symbol || a.Request.Side != q.Side || a.Request.Action != q.Action || a.Request.Amount != q.Amount {
		return nil, orders.ErrConflict
	}
	result := &normFill{OrderID: a.Signature, Status: a.State}
	if a.Fill == nil {
		return result, nil
	}
	amount, err := strconv.ParseFloat(a.Fill.BaseQuantity, 64)
	if err != nil {
		return nil, err
	}
	quote, err := strconv.ParseFloat(a.Fill.QuoteQuantity, 64)
	if err != nil {
		return nil, err
	}
	fee, err := strconv.ParseFloat(a.Fill.ExchangeFee, 64)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(amount) || math.IsInf(amount, 0) || math.IsNaN(quote) || math.IsInf(quote, 0) || math.IsNaN(fee) || math.IsInf(fee, 0) || amount < 0 || quote < 0 || fee < 0 {
		return nil, orders.ErrUncertain
	}
	result.FilledAmount, result.Fee, result.Status, result.Filled = amount, fee, a.Fill.Status, amount > 0
	if amount > 0 {
		base, ok := new(big.Rat).SetString(a.Fill.BaseQuantity)
		if !ok {
			return nil, orders.ErrUncertain
		}
		notional, ok := new(big.Rat).SetString(a.Fill.QuoteQuantity)
		if !ok {
			return nil, orders.ErrUncertain
		}
		result.AvgFillPrice, _ = new(big.Rat).Quo(notional, base).Float64()
		if !positiveLiveNumber(result.AvgFillPrice) {
			return nil, orders.ErrUncertain
		}
	}
	return result, nil
}

func (*phoenixAccountFeed) WaitForLeverage(context.Context, string, float64) error { return nil }
