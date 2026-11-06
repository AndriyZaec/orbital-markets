package api

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
	phoenixaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/delegation"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	phoenixrisk "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/risk"
)

var errPhoenixLiveDisabled = errors.New("Phoenix live execution is not enabled")

type PhoenixAccountBackend interface {
	phoenixaccount.RiskComputer
	AccountAuthority(context.Context, string, uint64) (delegation.Authority, error)
}

// PhoenixAccountConfig supplies shared read-only account/market sources. A full
// PhoenixOrderBackend also enables the internal preparation module, not HTTP
// execution admission or submission.
// Construct once at startup: risk parameters and REST backoff are shared across
// owners while the existing registry owns per-owner capacity and idle eviction.
type PhoenixAccountConfig struct {
	Backend    PhoenixAccountBackend
	Markets    phoenixrisk.MarketStream
	RESTURL    string
	WSURL      string
	HTTPClient *http.Client
}

type phoenixAccountFeedFactory struct {
	config PhoenixAccountConfig
	reader *phoenixaccount.Reader
	risk   *phoenixrisk.Source
	store  *executor.Store
	logger *slog.Logger
}

func newPhoenixAccountFactory(config PhoenixAccountConfig, store *executor.Store, logger *slog.Logger) *phoenixAccountFeedFactory {
	return &phoenixAccountFeedFactory{config: config, store: store, logger: logger,
		reader: phoenixaccount.NewReader(config.RESTURL, config.HTTPClient), risk: phoenixrisk.NewSource(config.RESTURL, config.HTTPClient, config.Markets)}
}

func (f *phoenixAccountFeedFactory) Normalize(owner string) (string, error) {
	owner = strings.TrimSpace(owner)
	if !phoenixaccount.ValidOwner(owner) {
		return "", errors.New("invalid Phoenix owner authority")
	}
	return owner, nil // Solana identities are case-sensitive.
}

func (f *phoenixAccountFeedFactory) Start(ctx context.Context, owner string) (liveAccountFeed, error) {
	if f.store == nil || f.config.Backend == nil || f.config.Markets == nil {
		return nil, errors.New("Phoenix account dependencies unavailable")
	}
	state, err := phoenixaccount.NewState(owner)
	if err != nil {
		return nil, err
	}
	feed := &phoenixAccountFeed{ctx: ctx, factory: f, state: state,
		subscriber: phoenixaccount.NewSubscriberWithURL(state, f.reader, f.logger, f.config.WSURL), wake: make(chan struct{}, 1)}
	go feed.run()
	return feed, nil
}

type phoenixAccountDetails struct {
	AccountReady       bool                        `json:"account_ready"`
	LiveReady          bool                        `json:"live_ready"`
	PositionAuthority  string                      `json:"position_authority,omitempty"`
	AuthorityCheckedAt *time.Time                  `json:"authority_checked_at,omitempty"`
	Authorized         bool                        `json:"authorized"`
	CanIncreaseRisk    bool                        `json:"can_increase_risk"`
	CanReduceRisk      bool                        `json:"can_reduce_risk"`
	Capabilities       phoenixaccount.Capabilities `json:"capabilities"`
	Positions          []liveAccountPosition       `json:"positions"`
	PositionsUpdatedAt *time.Time                  `json:"positions_updated_at,omitempty"`
	LiquidationModel   string                      `json:"liquidation_model,omitempty"`
	RiskTier           string                      `json:"risk_tier,omitempty"`
}

type phoenixAccountRefresh struct {
	done chan struct{}
	err  error
}
type phoenixAccountFeed struct {
	ctx                                context.Context
	factory                            *phoenixAccountFeedFactory
	state                              *phoenixaccount.State
	subscriber                         *phoenixaccount.Subscriber
	wake                               chan struct{}
	mu                                 sync.Mutex
	refresh                            *phoenixAccountRefresh
	snapshot                           liveAccountSnapshot
	riskResult                         helper.RiskResult
	marketGeneration, exchangeSequence uint64
	authorityAt                        time.Time
	lastAttempt                        time.Time
	authorityRevision                  uint64
}

func (f *phoenixAccountFeed) run() {
	go func() { _ = f.subscriber.Run(f.ctx) }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
		case <-f.subscriber.Updates():
		case <-f.wake:
		}
		f.mu.Lock()
		lastAttempt, authorityAt := f.lastAttempt, f.authorityAt
		f.mu.Unlock()
		if time.Since(lastAttempt) < 5*time.Second {
			continue
		}
		if !f.state.Snapshot().Fresh(time.Now()) {
			continue
		}
		if f.Snapshot().UnavailableReason == "" && time.Since(authorityAt) < 15*time.Second {
			continue
		}
		_ = f.refreshView(f.ctx)
	}
}

func (f *phoenixAccountFeed) refreshView(ctx context.Context) error {
	f.mu.Lock()
	r := f.refresh
	if r == nil {
		r = &phoenixAccountRefresh{done: make(chan struct{})}
		f.refresh = r
		f.lastAttempt = time.Now()
		revision := f.authorityRevision
		go func() {
			opCtx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
			defer cancel()
			snapshot, result, markets, authorityAt, err := f.readView(opCtx)
			f.mu.Lock()
			if revision != f.authorityRevision && err == nil {
				err = errors.New("Phoenix authority changed during verification")
			}
			if err == nil && f.ctx.Err() == nil && revision == f.authorityRevision {
				f.snapshot, f.riskResult, f.marketGeneration, f.exchangeSequence, f.authorityAt = snapshot, result, markets.Generation, markets.Sequence, authorityAt
			} else if revision == f.authorityRevision {
				f.snapshot = liveAccountSnapshot{Venue: "phoenix", Account: f.state.Snapshot().Owner, UnavailableReason: "Phoenix account verification unavailable"}
				if err != nil {
					f.snapshot.UnavailableReason = err.Error()
				}
			}
			r.err = err
			f.refresh = nil
			close(r.done)
			f.mu.Unlock()
		}()
	}
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return r.err
	}
}

func (f *phoenixAccountFeed) readView(ctx context.Context) (liveAccountSnapshot, helper.RiskResult, phoenix.MarketStreamSnapshot, time.Time, error) {
	failed := func(err error) (liveAccountSnapshot, helper.RiskResult, phoenix.MarketStreamSnapshot, time.Time, error) {
		return liveAccountSnapshot{}, helper.RiskResult{}, phoenix.MarketStreamSnapshot{}, time.Time{}, err
	}
	before := f.state.Snapshot()
	if !before.Fresh(time.Now()) {
		return failed(errors.New("Phoenix account snapshot is not fresh and synchronized"))
	}
	authorityAt := time.Now().UTC()
	authority, err := f.factory.config.Backend.AccountAuthority(ctx, before.Owner, before.Slot)
	if err != nil {
		return failed(errors.New("Phoenix on-chain authority could not be verified"))
	}
	if authority.Slot < before.Slot || !phoenixaccount.ValidOwner(authority.PositionAuthority) || authority.PositionAuthority == before.Owner {
		return failed(errors.New("Phoenix trading agent authorization required"))
	}
	authorized, err := f.factory.store.AgentAuthorizationMatches(ctx, "phoenix", before.Owner, authority.PositionAuthority)
	if err != nil || !authorized {
		return failed(errors.New("Phoenix current delegate is not the registered Orbital agent"))
	}
	markets := f.factory.config.Markets.Snapshot()
	result, err := f.factory.risk.Evaluate(ctx, f.state, f.factory.config.Backend)
	if err != nil {
		return failed(err)
	}
	after := f.state.Snapshot()
	currentMarkets := f.factory.config.Markets.Snapshot()
	if !after.Fresh(time.Now()) || after.Generation != before.Generation || after.Sequence != before.Sequence || after.Slot != before.Slot ||
		!currentMarkets.Connected || !currentMarkets.Synchronized || markets.Generation != currentMarkets.Generation || markets.Sequence != currentMarkets.Sequence {
		return failed(errors.New("Phoenix account or exchange changed during verification"))
	}
	authorized, err = f.factory.store.AgentAuthorizationMatches(ctx, "phoenix", before.Owner, authority.PositionAuthority)
	if err != nil || !authorized {
		return failed(errors.New("Phoenix agent authorization changed during verification"))
	}
	positions, err := normalizePhoenixPositions(after, result, currentMarkets)
	if err != nil {
		return failed(err)
	}
	equity, err := phoenixQuoteValue(result.EquityQuoteLots)
	if err != nil {
		return failed(err)
	}
	available, err := phoenixQuoteValue(result.AvailableMarginQuoteLots)
	if err != nil {
		return failed(err)
	}
	checkedAt := authorityAt
	details := &phoenixAccountDetails{PositionAuthority: authority.PositionAuthority, AuthorityCheckedAt: &checkedAt, Authorized: true,
		Capabilities: after.Cross.Capabilities, Positions: positions, LiquidationModel: result.LiquidationModel, RiskTier: result.RiskTier}
	details.CanIncreaseRisk = phoenixAccess(details.Capabilities.Actions.RiskIncreasingTrade) && result.RiskTier == "Safe"
	details.CanReduceRisk = phoenixAccess(details.Capabilities.Actions.RiskReducingTrade)
	reason := ""
	capableState := details.Capabilities.State == "active" || details.Capabilities.State == "cold" || details.Capabilities.State == "reduceOnly"
	if !capableState || !phoenixAccess(details.Capabilities.Actions.PlaceMarketOrder) || (!details.CanIncreaseRisk && !details.CanReduceRisk) {
		reason = "Phoenix account trading capabilities unavailable"
	}
	details.AccountReady = reason == ""
	updated := after.PositionsUpdatedAt
	details.PositionsUpdatedAt = &updated
	observed := after.ObservedAt
	if authorityAt.Before(observed) {
		observed = authorityAt
	}
	if source := time.UnixMilli(result.AsOfUnixMS); source.Before(observed) {
		observed = source
	}
	return liveAccountSnapshot{Venue: "phoenix", Account: before.Owner, Connected: true, LastUpdated: observed, PositionsUpdatedAt: after.PositionsUpdatedAt,
		Equity: equity, Available: available, Positions: positions, UnavailableReason: reason, Phoenix: details}, result, currentMarkets, authorityAt, nil
}

func (f *phoenixAccountFeed) Snapshot() liveAccountSnapshot {
	state := f.state.Snapshot()
	markets := f.factory.config.Markets.Snapshot()
	f.mu.Lock()
	snapshot, result, authorityAt, marketGeneration, sequence := f.snapshot, f.riskResult, f.authorityAt, f.marketGeneration, f.exchangeSequence
	f.mu.Unlock()
	snapshot.Venue, snapshot.Account = "phoenix", state.Owner
	if snapshot.Phoenix != nil {
		details := *snapshot.Phoenix
		details.Positions = append([]liveAccountPosition{}, details.Positions...)
		if details.AuthorityCheckedAt != nil {
			at := *details.AuthorityCheckedAt
			details.AuthorityCheckedAt = &at
		}
		if details.PositionsUpdatedAt != nil {
			at := *details.PositionsUpdatedAt
			details.PositionsUpdatedAt = &at
		}
		snapshot.Phoenix = &details
	}
	reason := snapshot.UnavailableReason
	verified := false
	switch {
	case f.ctx.Err() != nil:
		reason = "Phoenix account feed stopped"
	case !state.Fresh(time.Now()):
		reason = "Phoenix account snapshot is not fresh and synchronized"
	case reason != "" && snapshot.Phoenix == nil:
		// Preserve actionable authority/risk read errors when no verified view
		// was published, instead of masking them with a generic revision error.
	case !markets.Connected || !markets.Synchronized || markets.Generation != marketGeneration || markets.Sequence != sequence:
		reason = "Phoenix market state requires risk resynchronization"
	case result.AccountGeneration != state.Generation || result.AccountSlot != state.Slot || result.AccountSequence != state.Sequence:
		reason = "Phoenix account changed; risk refresh required"
	case !phoenixTimeFresh(authorityAt, time.Now(), admissionFreshness):
		reason = "Phoenix authority verification is stale"
	case result.ValidUntilUnixMS < time.Now().UnixMilli():
		reason = "Phoenix risk inputs are stale"
	default:
		verified = true
	}
	if reason != "" {
		snapshot.UnavailableReason = reason
		snapshot.PositionsUpdatedAt = time.Time{}
		if snapshot.Phoenix != nil {
			snapshot.Phoenix.AccountReady = false
			if !verified {
				snapshot.Phoenix.Authorized = false
			}
			snapshot.Phoenix.CanIncreaseRisk = false
			snapshot.Phoenix.CanReduceRisk = false
			snapshot.Phoenix.PositionsUpdatedAt = nil
		}
	}
	if snapshot.Phoenix == nil {
		snapshot.Phoenix = &phoenixAccountDetails{Positions: []liveAccountPosition{}}
	}
	snapshot.Positions = append([]liveAccountPosition{}, snapshot.Positions...)
	return snapshot
}

func (f *phoenixAccountFeed) invalidateAuthority() {
	f.mu.Lock()
	f.authorityRevision++
	f.authorityAt = time.Time{}
	f.snapshot.UnavailableReason = "Phoenix authority confirmation required"
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *phoenixAccountFeed) RefreshPositions(ctx context.Context) error {
	if err := f.subscriber.Refresh(ctx); err != nil {
		return err
	}
	return f.refreshView(ctx)
}

// ProjectOpen is an internal planning seam. The feed still rejects every live
// submit; a model's margin_sufficient is not transaction authorization.
func (f *phoenixAccountFeed) ProjectOpen(ctx context.Context, order helper.OrderProjection) (helper.RiskResult, error) {
	if err := f.refreshView(ctx); err != nil {
		return helper.RiskResult{}, err
	}
	before := f.Snapshot()
	if before.UnavailableReason != "" || before.Phoenix == nil || !before.Phoenix.AccountReady || !before.Phoenix.CanIncreaseRisk {
		return helper.RiskResult{}, errors.New("Phoenix account is not ready for opening projection")
	}
	f.mu.Lock()
	revision := f.authorityRevision
	f.mu.Unlock()
	result, err := f.factory.risk.ProjectOpen(ctx, f.state, f.factory.config.Backend, order)
	if err != nil {
		return helper.RiskResult{}, err
	}
	after := f.Snapshot()
	f.mu.Lock()
	changed := f.authorityRevision != revision
	f.mu.Unlock()
	if changed || after.UnavailableReason != "" || after.Phoenix == nil || !after.Phoenix.Authorized || before.Phoenix.PositionAuthority != after.Phoenix.PositionAuthority {
		return helper.RiskResult{}, errors.New("Phoenix authority or account changed during opening projection")
	}
	return result, nil
}
func (f *phoenixAccountFeed) PreTradeBlockers(domain.Leg) []string {
	reasons := []string{errPhoenixLiveDisabled.Error()}
	if reason := f.Snapshot().UnavailableReason; reason != "" {
		reasons = append(reasons, reason)
	}
	return reasons
}
func (*phoenixAccountFeed) SubmitSigned(context.Context, domain.SignedAction, *domain.SigningRequest) (*domain.SubmissionResult, error) {
	return nil, errPhoenixLiveDisabled
}
func (*phoenixAccountFeed) WaitForFill(context.Context, *domain.SigningRequest) (*normFill, error) {
	return nil, errPhoenixLiveDisabled
}
func (*phoenixAccountFeed) WaitForLeverage(context.Context, string, float64) error {
	return errPhoenixLiveDisabled
}

func phoenixAccess(access phoenixaccount.CapabilityAccess) bool {
	return access.Immediate || access.ViaColdActivation
}
func phoenixTimeFresh(at, now time.Time, budget time.Duration) bool {
	return !at.IsZero() && !now.Before(at) && now.Sub(at) <= budget
}
func phoenixQuoteValue(value string) (float64, error) {
	v, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("invalid Phoenix risk amount")
	}
	return v / 1_000_000, nil
}

func normalizePhoenixPositions(state phoenixaccount.Snapshot, risk helper.RiskResult, markets phoenix.MarketStreamSnapshot) ([]liveAccountPosition, error) {
	metadata := make(map[string]phoenix.MarketMetadata, len(markets.Metadata.Markets))
	for _, m := range markets.Metadata.Markets {
		metadata[m.Symbol] = m
	}
	positions := make([]liveAccountPosition, 0, len(state.Cross.Positions))
	for symbol, p := range state.Cross.Positions {
		lots, err := strconv.ParseInt(p.BasePositionLots, 10, 64)
		if err != nil {
			return nil, err
		}
		if lots == 0 {
			continue
		}
		market, ok := metadata[symbol]
		if !ok || market.AmountIncrement <= 0 {
			return nil, errors.New("Phoenix position precision unavailable")
		}
		entry, err := strconv.ParseFloat(p.EntryPriceUSD, 64)
		if err != nil || entry <= 0 || math.IsNaN(entry) || math.IsInf(entry, 0) {
			return nil, errors.New("Phoenix position entry unavailable")
		}
		size := math.Abs(float64(lots)) * market.AmountIncrement
		if size <= 0 || math.IsNaN(size) || math.IsInf(size, 0) {
			return nil, errors.New("invalid Phoenix position size")
		}
		side := "long"
		if lots < 0 {
			side = "short"
		}
		liq := 0.0
		if price := risk.StaticLiquidationPricesUSD[symbol]; price != nil {
			liq = *price
		}
		positions = append(positions, liveAccountPosition{Symbol: symbol, Side: side, Size: size, EntryPrice: entry, LiqPrice: liq, BaseLots: p.BasePositionLots, LiquidationModel: risk.LiquidationModel})
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i].Symbol < positions[j].Symbol })
	return positions, nil
}
