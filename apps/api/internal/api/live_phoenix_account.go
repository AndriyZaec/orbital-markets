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
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"
	phoenixrisk "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/risk"
)

var errPhoenixLiveDisabled = errors.New("Phoenix live execution is not enabled")

type PhoenixAccountBackend interface {
	phoenixaccount.RiskComputer
}

// PhoenixAccountConfig supplies shared account/market sources. Full execution
// requires journaled submission plus infrastructure and Flight readiness.
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
	config       PhoenixAccountConfig
	reader       *phoenixaccount.Reader
	risk         *phoenixrisk.Source
	store        *executor.Store
	logger       *slog.Logger
	orderJournal *orders.Store
	orders       *orders.Service
}

func newPhoenixAccountFactory(config PhoenixAccountConfig, store *executor.Store, logger *slog.Logger) *phoenixAccountFeedFactory {
	f := &phoenixAccountFeedFactory{config: config, store: store, logger: logger,
		reader: phoenixaccount.NewReader(config.RESTURL, config.HTTPClient), risk: phoenixrisk.NewSource(config.RESTURL, config.HTTPClient, config.Markets)}
	if store != nil {
		journal, err := store.PhoenixOrderJournal()
		if err == nil {
			f.orderJournal = journal
			if backend, ok := config.Backend.(PhoenixSubmitBackend); ok {
				f.orders = orders.NewService(journal, backend, orders.NewHistory(config.RESTURL, config.HTTPClient))
			}
		}
	}
	return f
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
	AccountReady               bool   `json:"account_ready"`
	LiveReady                  bool   `json:"live_ready"`
	LiveUnavailableReason      string `json:"live_unavailable_reason,omitempty"`
	orderOutcomeUnknown        bool
	RegisteredAgent            string                      `json:"registered_agent,omitempty"`
	AuthorizationRecorded      bool                        `json:"authorization_recorded"`
	ActionVerificationRequired bool                        `json:"action_verification_required"`
	CanIncreaseRisk            bool                        `json:"can_increase_risk"`
	CanReduceRisk              bool                        `json:"can_reduce_risk"`
	Capabilities               phoenixaccount.Capabilities `json:"capabilities"`
	Positions                  []liveAccountPosition       `json:"positions"`
	PositionsUpdatedAt         *time.Time                  `json:"positions_updated_at,omitempty"`
	LiquidationModel           string                      `json:"liquidation_model,omitempty"`
	RiskTier                   string                      `json:"risk_tier,omitempty"`
	PendingFundingQuoteLots    string                      `json:"pending_funding_quote_lots,omitempty"`
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
	viewAt                             time.Time
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
		lastAttempt, viewAt := f.lastAttempt, f.viewAt
		f.mu.Unlock()
		if time.Since(lastAttempt) < 5*time.Second {
			continue
		}
		if !f.state.Snapshot().Fresh(time.Now()) {
			continue
		}
		if f.Snapshot().UnavailableReason == "" && time.Since(viewAt) < 15*time.Second {
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
			snapshot, result, markets, viewAt, err := f.readView(opCtx)
			f.mu.Lock()
			if revision != f.authorityRevision && err == nil {
				err = errors.New("Phoenix authority changed during verification")
			}
			if err == nil && f.ctx.Err() == nil && revision == f.authorityRevision {
				f.snapshot, f.riskResult, f.marketGeneration, f.exchangeSequence, f.viewAt = snapshot, result, markets.Generation, markets.Sequence, viewAt
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
	viewAt := time.Now().UTC()
	agent, err := f.factory.store.RegisteredAgent(ctx, "phoenix", before.Owner)
	if err != nil {
		return failed(errors.New("Phoenix agent registration unavailable"))
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
	currentAgent, err := f.factory.store.RegisteredAgent(ctx, "phoenix", before.Owner)
	if err != nil || currentAgent != agent {
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
	details := &phoenixAccountDetails{RegisteredAgent: agent, AuthorizationRecorded: agent != "" && agent != before.Owner && phoenixaccount.ValidOwner(agent), ActionVerificationRequired: true,
		Capabilities: after.Cross.Capabilities, Positions: positions, LiquidationModel: result.LiquidationModel, RiskTier: result.RiskTier, PendingFundingQuoteLots: result.UnsettledFundingQuoteLots}
	details.CanIncreaseRisk = phoenixAccess(details.Capabilities.Actions.RiskIncreasingTrade) && result.RiskTier == "Safe"
	details.CanReduceRisk = phoenixAccess(details.Capabilities.Actions.RiskReducingTrade)
	reason := ""
	capableState := details.Capabilities.State == "active" || details.Capabilities.State == "cold" || details.Capabilities.State == "reduceOnly"
	if !capableState || !phoenixAccess(details.Capabilities.Actions.PlaceMarketOrder) || (!details.CanIncreaseRisk && !details.CanReduceRisk) {
		reason = "Phoenix account trading capabilities unavailable"
	}
	details.AccountReady = reason == ""
	if f.factory.orderJournal != nil {
		pending, err := f.factory.orderJournal.Unresolved(ctx, before.Owner)
		details.orderOutcomeUnknown = err != nil || pending
	}
	updated := after.PositionsUpdatedAt
	details.PositionsUpdatedAt = &updated
	observed := after.ObservedAt
	if viewAt.Before(observed) {
		observed = viewAt
	}
	if source := time.UnixMilli(result.AsOfUnixMS); source.Before(observed) {
		observed = source
	}
	return liveAccountSnapshot{Venue: "phoenix", Account: before.Owner, Connected: true, LastUpdated: observed, PositionsUpdatedAt: after.PositionsUpdatedAt,
		Equity: equity, Available: available, Positions: positions, UnavailableReason: reason, Phoenix: details}, result, currentMarkets, viewAt, nil
}

func (f *phoenixAccountFeed) Snapshot() liveAccountSnapshot {
	state := f.state.Snapshot()
	markets := f.factory.config.Markets.Snapshot()
	f.mu.Lock()
	snapshot, result, marketGeneration, sequence := f.snapshot, f.riskResult, f.marketGeneration, f.exchangeSequence
	f.mu.Unlock()
	snapshot.Venue, snapshot.Account = "phoenix", state.Owner
	if snapshot.Phoenix != nil {
		details := *snapshot.Phoenix
		details.Positions = append([]liveAccountPosition{}, details.Positions...)
		if details.PositionsUpdatedAt != nil {
			at := *details.PositionsUpdatedAt
			details.PositionsUpdatedAt = &at
		}
		snapshot.Phoenix = &details
	}
	reason := snapshot.UnavailableReason
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
	case result.ValidUntilUnixMS < time.Now().UnixMilli():
		reason = "Phoenix risk inputs are stale"
	}
	if reason != "" {
		snapshot.UnavailableReason = reason
		snapshot.PositionsUpdatedAt = time.Time{}
		if snapshot.Phoenix != nil {
			snapshot.Phoenix.AccountReady = false
			snapshot.Phoenix.CanIncreaseRisk = false
			snapshot.Phoenix.CanReduceRisk = false
			snapshot.Phoenix.PositionsUpdatedAt = nil
		}
	}
	if snapshot.Phoenix == nil {
		snapshot.Phoenix = &phoenixAccountDetails{Positions: []liveAccountPosition{}}
	}
	snapshot.Phoenix.LiveUnavailableReason = f.executionReadiness(snapshot.Phoenix, snapshot.UnavailableReason)
	snapshot.Phoenix.LiveReady = snapshot.Phoenix.LiveUnavailableReason == ""
	snapshot.Positions = append([]liveAccountPosition{}, snapshot.Positions...)
	return snapshot
}

func (f *phoenixAccountFeed) invalidateAuthority() {
	f.mu.Lock()
	f.authorityRevision++
	f.viewAt = time.Time{}
	f.snapshot.UnavailableReason = "Phoenix agent registration refresh required"
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

// ProjectOpen is a planning seam, not transaction authorization.
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
	if changed || after.UnavailableReason != "" || after.Phoenix == nil || !after.Phoenix.AuthorizationRecorded || before.Phoenix.RegisteredAgent != after.Phoenix.RegisteredAgent {
		return helper.RiskResult{}, errors.New("Phoenix authority or account changed during opening projection")
	}
	return result, nil
}
func (f *phoenixAccountFeed) PreTradeBlockers(domain.Leg) []string {
	snapshot := f.Snapshot()
	if snapshot.Phoenix == nil {
		return []string{"Phoenix account verification required"}
	}
	if !snapshot.Phoenix.LiveReady {
		return []string{snapshot.Phoenix.LiveUnavailableReason}
	}
	return nil
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
		positions = append(positions, liveAccountPosition{Symbol: symbol, Side: side, Size: size, EntryPrice: entry, LiqPrice: liq, BaseLots: p.BasePositionLots, LiquidationModel: risk.LiquidationModel, PendingFundingQuoteLots: p.UnsettledFundingQuoteLots})
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i].Symbol < positions[j].Symbol })
	return positions, nil
}
