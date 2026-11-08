package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/delegation"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/gorilla/websocket"
)

const phoenixTestOwner = "11111111111111111111111111111111"
const phoenixTestAgent = "So11111111111111111111111111111111111111112"

type phoenixTestMarkets struct{ disconnected atomic.Bool }

func (m *phoenixTestMarkets) Snapshot() phoenix.MarketStreamSnapshot {
	now := time.Now().UTC()
	return phoenix.MarketStreamSnapshot{Connected: !m.disconnected.Load(), Synchronized: !m.disconnected.Load(), Generation: 1, Sequence: 42,
		Metadata: phoenix.ExchangeMetadata{Markets: []phoenix.MarketMetadata{{Symbol: "SOL", Status: "active", AmountIncrement: .01, PriceIncrement: .01, MaxLeverage: 25}, {Symbol: "BTC", Status: "active", AmountIncrement: .0001, PriceIncrement: 1, MaxLeverage: 20}}},
		Stats:    map[string]phoenix.LiveMarketStats{"SOL": {Symbol: "SOL", MarkPrice: 100, IndexPrice: 90, Generation: 1, SourceTime: now, ReceivedAt: now}, "BTC": {Symbol: "BTC", MarkPrice: 50000, IndexPrice: 49000, Generation: 1, SourceTime: now, ReceivedAt: now}}}
}

type phoenixTestBackend struct {
	authorityCalls      atomic.Int32
	mu                  sync.Mutex
	agent               string
	riskErr             error
	authorityErr        error
	minSlot             uint64
	onRisk              func()
	onProjection        func()
	projectedMultiplier float64
}

func (b *phoenixTestBackend) AccountAuthority(_ context.Context, owner string, slot uint64) (delegation.Authority, error) {
	b.authorityCalls.Add(1)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.minSlot = slot
	return delegation.Authority{PositionAuthority: b.agent, Slot: 500}, b.authorityErr
}
func (b *phoenixTestBackend) ComputeRisk(_ context.Context, q helper.RiskRequest) (helper.RiskResult, error) {
	b.mu.Lock()
	err, callback := b.riskErr, b.onRisk
	b.mu.Unlock()
	if err != nil {
		return helper.RiskResult{}, err
	}
	if callback != nil {
		callback()
	}
	expires, err := helper.RiskExpiry(q)
	if err != nil {
		return helper.RiskResult{}, err
	}
	prices := make(map[string]*float64)
	for _, p := range q.Positions {
		if p.BaseLots != "0" {
			prices[p.Symbol] = nil
		}
	}
	result := helper.RiskResult{OwnerAuthority: q.OwnerAuthority, AccountGeneration: q.AccountGeneration, AccountSlot: q.AccountSlot, AccountSequence: q.AccountSequence,
		MetadataGeneration: q.MetadataGeneration, AsOfUnixMS: q.AsOfUnixMS, ValidUntilUnixMS: expires, QuoteDecimals: 6,
		EquityQuoteLots: "199999877", EffectiveCollateralQuoteLots: "181999877", AvailableMarginQuoteLots: "179499877", InitialMarginQuoteLots: "2500000", MaintenanceMarginQuoteLots: "1250000",
		UnsettledFundingQuoteLots: "-123", UnrealizedPnLQuoteLots: "10000000", SpotNotionalQuoteLots: "90000000", DiscountedSpotQuoteLots: "72000000",
		RiskTier: "Safe", LiquidationModel: "rise_local_static", StaticLiquidationPricesUSD: prices}
	if p := q.Projection; p != nil {
		b.mu.Lock()
		b.projectedMultiplier = p.TakerFeeMultiplier
		callback := b.onProjection
		b.mu.Unlock()
		if callback != nil {
			callback()
		}
		result.Projection = &helper.ProjectionResult{IntentID: p.IntentID, Symbol: p.Symbol, Side: p.Side, NumBaseLots: p.NumBaseLots, PriceLimitTicks: p.PriceLimitTicks, FlightFeeBPS: 2,
			Model: "rise_full_fill_at_limit", FeePolicy: "undiscounted_taker_ceiling", NotionalQuoteLots: "100000000", ExchangeFeeQuoteLots: "35000", FlightFeeQuoteLots: "20000", TotalFeeQuoteLots: "55000",
			ProjectedBaseLots: "100", ProjectedEquityQuoteLots: "199944877", ProjectedEffectiveCollateralQuoteLots: "181944877", ProjectedInitialMarginQuoteLots: "6500000", ProjectedMaintenanceMarginQuoteLots: "3250000", ProjectedAvailableMarginQuoteLots: "175444877", ProjectedRiskTier: "Safe", MarginSufficient: true}
	}
	return result, nil
}

type phoenixAccountFixture struct {
	server            *Server
	registry          *accountFeedRegistry
	factory           *phoenixAccountFeedFactory
	backend           *phoenixTestBackend
	markets           *phoenixTestMarkets
	store             *executor.Store
	wsCount, wsActive atomic.Int32
	send              chan map[string]any
	cancel            context.CancelFunc
	caps              map[string]any
}

func newPhoenixAccountFixture(t *testing.T) *phoenixAccountFixture {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	database, err := appdb.Open(filepath.Join(t.TempDir(), "phoenix-account.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	f := &phoenixAccountFixture{backend: &phoenixTestBackend{agent: phoenixTestAgent}, markets: &phoenixTestMarkets{}, store: executor.NewStore(database, database, logger), send: make(chan map[string]any, 4), cancel: cancel}
	if err := f.store.UpsertAgentAuthorization(ctx, "phoenix", phoenixTestOwner, phoenixTestAgent); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../venue/phoenix/account/testdata/trader-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var rest map[string]any
	if err := json.Unmarshal(raw, &rest); err != nil {
		t.Fatal(err)
	}
	snapshot := rest["snapshot"].(map[string]any)
	sub := snapshot["subaccounts"].([]any)[0].(map[string]any)
	sub["collateral"] = "100000000"
	sub["splines"] = []any{}
	sub["spotCollaterals"].([]any)[0].(map[string]any)["assetIndex"] = uint32(4294901760)
	position := sub["positions"].([]any)[0].(map[string]any)
	position["basePositionLots"] = "-10"
	position["virtualQuotePositionLots"] = "60000000"
	position["entryPriceUsd"] = "60000"
	f.caps = snapshot["capabilities"].(map[string]any)
	wsSnapshot := make(map[string]any)
	for key, value := range snapshot {
		wsSnapshot[key] = value
	}
	wsSnapshot["channel"] = "traderState"
	wsSnapshot["messageType"] = "snapshot"
	wsSnapshot["authority"] = phoenixTestOwner
	wsSnapshot["traderPdaIndex"] = 0
	wsSnapshot["slot"] = 100
	parameters, err := os.ReadFile("../venue/phoenix/risk/testdata/parameters.json")
	if err != nil {
		t.Fatal(err)
	}
	collateral, err := os.ReadFile("../venue/phoenix/risk/testdata/collateral.json")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/view/exchange":
			_, _ = w.Write(parameters)
		case "/v1/collateral/assets":
			_, _ = w.Write(collateral)
		case "/v1/trader/state/" + phoenixTestOwner:
			_ = json.NewEncoder(w).Encode(rest)
		case "/v1/ws":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			f.wsCount.Add(1)
			f.wsActive.Add(1)
			defer f.wsActive.Add(-1)
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			if err := conn.WriteJSON(wsSnapshot); err != nil {
				return
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return
				case <-done:
					return
				case message := <-f.send:
					if err := conn.WriteJSON(message); err != nil {
						return
					}
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(httpServer.Close)
	t.Cleanup(cancel)
	f.factory = newPhoenixAccountFactory(PhoenixAccountConfig{Backend: f.backend, Markets: f.markets, RESTURL: httpServer.URL, WSURL: "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/ws", HTTPClient: httpServer.Client()}, f.store, logger)
	f.registry = newAccountFeedRegistry(ctx, map[string]accountFeedFactory{"phoenix": f.factory}, accountFeedRegistryConfig{IdleTTL: time.Minute, MaxFeeds: 2, MaxPerVenue: 1})
	f.server = &Server{live: &LiveDeps{accounts: f.registry}}
	return f
}

func waitPhoenix(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !check() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("Phoenix account condition not reached")
		}
	}
}
func (f *phoenixAccountFixture) ready(t *testing.T) (*accountFeedLease, *phoenixAccountFeed) {
	t.Helper()
	lease, err := f.registry.Acquire("phoenix", phoenixTestOwner)
	if err != nil {
		t.Fatal(err)
	}
	feed := lease.Feed().(*phoenixAccountFeed)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("feed=%+v state=%+v", feed.Snapshot(), feed.state.Snapshot())
		}
	})
	waitPhoenix(t, func() bool { return feed.Snapshot().UnavailableReason == "" })
	return lease, feed
}

func TestPhoenixAccountRegistryBalancesAndReadOnlyBoundary(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	response := httptest.NewRecorder()
	f.server.handleLiveAccountsEnsure(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/accounts/ensure", strings.NewReader(`{"accounts":{"phoenix":"`+phoenixTestOwner+`"}}`)))
	if response.Code != 200 {
		t.Fatalf("ensure=%d %s", response.Code, response.Body.String())
	}
	lease, feed := f.ready(t)
	defer lease.Release()
	other, err := f.registry.Acquire("phoenix", " "+phoenixTestOwner+" ")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	if other.Feed() != feed || f.wsCount.Load() != 1 {
		t.Fatal("registry started duplicate owner subscriptions")
	}
	q := url.Values{"accounts[phoenix]": {phoenixTestOwner}}
	response = httptest.NewRecorder()
	f.server.handleLiveBalances(response, httptest.NewRequest(http.MethodGet, "/api/v1/live/balances?"+q.Encode(), nil))
	var statuses map[string]venueAccountStatus
	if err := json.Unmarshal(response.Body.Bytes(), &statuses); err != nil {
		t.Fatal(err)
	}
	status := statuses["phoenix"]
	if !status.Fresh || status.Phoenix == nil || !status.Phoenix.AccountReady || !status.Phoenix.AuthorizationRecorded || !status.Phoenix.ActionVerificationRequired || status.Phoenix.LiveReady || !status.Phoenix.CanReduceRisk || len(status.Phoenix.Positions) != 1 {
		t.Fatalf("unexpected readiness: %s", response.Body.String())
	}
	position := status.Phoenix.Positions[0]
	if position.Side != "short" || position.Size != .001 || position.EntryPrice != 60000 || position.BaseLots != "-10" || position.LiquidationModel != "rise_local_static" {
		t.Fatalf("wrong normalized position: %+v", position)
	}
	if status.Equity != 199.999877 || status.Available != 179.499877 {
		t.Fatal("wrong quote-unit normalization")
	}
	if _, err := feed.SubmitSigned(context.Background(), domain.SignedAction{}, nil); !errors.Is(err, errPhoenixLiveDisabled) {
		t.Fatal("read-only feed submitted")
	}
	if _, err := feed.WaitForFill(context.Background(), nil); !errors.Is(err, errPhoenixLiveDisabled) {
		t.Fatal("read-only feed confirmed a fill")
	}
	if len(feed.PreTradeBlockers(domain.Leg{})) == 0 {
		t.Fatal("account readiness enabled opening")
	}
	f.backend.mu.Lock()
	slot := f.backend.minSlot
	f.backend.mu.Unlock()
	if slot != 0 || f.backend.authorityCalls.Load() != 0 {
		t.Fatal("account ensure/balances issued idle authority RPC")
	}
}

func TestPhoenixAccountRefreshDoesNotPollChainAndKeepsRegistrationSeparate(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	defer lease.Release()
	f.backend.mu.Lock()
	f.backend.agent = phoenixTestOwner
	f.backend.authorityErr = errors.New("RPC unavailable")
	f.backend.mu.Unlock()
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal("passive account view required RPC", err)
	}
	if got := feed.Snapshot(); !got.Phoenix.AccountReady || !got.Phoenix.ActionVerificationRequired || got.Phoenix.RegisteredAgent != phoenixTestAgent || f.backend.authorityCalls.Load() != 0 {
		t.Fatal("passive view claimed fresh authority or polled chain", got.Phoenix)
	}
	f.backend.mu.Lock()
	f.backend.agent = phoenixTestAgent
	f.backend.mu.Unlock()
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteAgentAuthorization(context.Background(), "phoenix", phoenixTestOwner, phoenixTestAgent); err != nil {
		t.Fatal(err)
	}
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := feed.Snapshot(); !got.Phoenix.AccountReady || got.Phoenix.AuthorizationRecorded || got.Phoenix.LiveReady {
		t.Fatal("missing registration hid account data or granted trading readiness", got.Phoenix)
	}
	if err := f.store.UpsertAgentAuthorization(context.Background(), "phoenix", phoenixTestOwner, phoenixTestAgent); err != nil {
		t.Fatal(err)
	}
	f.backend.mu.Lock()
	f.backend.riskErr = errors.New("risk unavailable")
	f.backend.mu.Unlock()
	if err := feed.refreshView(context.Background()); err == nil {
		t.Fatal("risk outage reused a successful view")
	}
}

func TestPhoenixAccountGapMarketDisconnectAndExpiryInvalidateCachedView(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	defer lease.Release()
	f.markets.disconnected.Store(true)
	if feed.Snapshot().UnavailableReason == "" {
		t.Fatal("market disconnect reused scanner fallback")
	}
	f.markets.disconnected.Store(false)
	feed.mu.Lock()
	feed.riskResult.ValidUntilUnixMS = time.Now().Add(-time.Second).UnixMilli()
	feed.mu.Unlock()
	if feed.Snapshot().UnavailableReason == "" {
		t.Fatal("expired risk remained ready")
	}
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.send <- map[string]any{"channel": "traderState", "authority": phoenixTestOwner, "traderPdaIndex": 0, "slot": 101, "messageType": "delta", "deltas": []any{map[string]any{"subaccountIndex": 0, "sequence": 43, "collateral": "100000000"}}}
	waitPhoenix(t, func() bool { return !feed.state.Snapshot().Synchronized })
	if got := feed.Snapshot(); got.UnavailableReason == "" || !got.PositionsUpdatedAt.IsZero() || got.Phoenix.AccountReady {
		t.Fatal("sequence gap left authoritative position state")
	}
}

func TestPhoenixKnownAuthorityMutationFencesInflightRefresh(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	defer lease.Release()
	f.backend.mu.Lock()
	f.backend.onRisk = func() { f.server.invalidatePhoenixAccount(phoenixTestOwner) }
	f.backend.mu.Unlock()
	if err := feed.refreshView(context.Background()); err == nil {
		t.Fatal("known delegation mutation allowed an earlier authority read to publish")
	}
	if got := feed.Snapshot(); got.UnavailableReason == "" || got.Phoenix.AccountReady {
		t.Fatal("invalidated authority remained ready")
	}
}

func TestPhoenixCapabilitiesAndRegistryEviction(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	f.send <- map[string]any{"channel": "traderState", "authority": phoenixTestOwner, "traderPdaIndex": 0, "slot": 101, "messageType": "delta", "deltas": []any{map[string]any{
		"subaccountIndex": 0, "sequence": 42, "collateral": "100000000", "capabilities": map[string]any{"flags": 0, "state": "frozen", "capabilities": map[string]any{}}}}}
	waitPhoenix(t, func() bool { return feed.state.Snapshot().Sequence == 42 })
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := feed.Snapshot(); got.Phoenix.AccountReady || !got.Phoenix.AuthorizationRecorded || got.Phoenix.CanIncreaseRisk || got.Phoenix.CanReduceRisk {
		t.Fatalf("frozen readiness: %+v", got.Phoenix)
	}
	f.send <- map[string]any{"channel": "traderState", "authority": phoenixTestOwner, "traderPdaIndex": 0, "slot": 102, "messageType": "delta", "deltas": []any{map[string]any{
		"subaccountIndex": 0, "sequence": 43, "collateral": "100000000", "capabilities": map[string]any{"flags": 1, "state": "reduceOnly", "capabilities": map[string]any{
			"placeMarketOrder": map[string]any{"immediate": true}, "riskReducingTrade": map[string]any{"immediate": true}}}}}}
	waitPhoenix(t, func() bool { return feed.state.Snapshot().Sequence == 43 })
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := feed.Snapshot(); !got.Phoenix.AccountReady || got.Phoenix.CanIncreaseRisk || !got.Phoenix.CanReduceRisk || got.Phoenix.LiveReady {
		t.Fatalf("reduce-only readiness: %+v", got.Phoenix)
	}
	lease.Release()
	f.registry.cleanupIdle(time.Now().Add(2 * time.Minute))
	waitPhoenix(t, func() bool { return f.wsActive.Load() == 0 })
	if got := feed.Snapshot(); got.UnavailableReason != "Phoenix account feed stopped" || got.Phoenix.AccountReady {
		t.Fatal("idle eviction did not stop the feed")
	}
}

func TestPhoenixStartupRegistersAccountFactoryWithoutLiveModule(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps := NewLiveDeps(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), domain.NewSigningRequestStore(), f.store, nil, nil, nil, nil, f.factory.config)
	if _, ok := deps.accounts.factories["phoenix"]; !ok {
		t.Fatal("missing account factory")
	}
	if _, err := deps.liveModule("phoenix"); err == nil {
		t.Fatal("read-only account setup registered live execution")
	}
	if err := (liveVenueBindings{Accounts: map[string]string{"phoenix": phoenixTestOwner}}).requireAccountsWithin(supportedLiveVenues); err != nil {
		t.Fatal("Phoenix is missing from execution bindings", err)
	}
}

func TestPhoenixOpeningProjectionDoesNotEnableSubmitAndFencesAuthority(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	defer lease.Release()
	order := helper.OrderProjection{IntentID: "open-1", Symbol: "SOL", Side: "bid", NumBaseLots: "100", PriceLimitTicks: "10000", QuoteObservedUnixMS: time.Now().UnixMilli(), FlightFeeBPS: 2, TakerFeeMultiplier: 999}
	result, err := feed.ProjectOpen(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	if result.Projection == nil || !result.Projection.MarginSufficient || feed.Snapshot().Phoenix.LiveReady {
		t.Fatal("projection changed live trading readiness")
	}
	f.backend.mu.Lock()
	multiplier := f.backend.projectedMultiplier
	f.backend.onProjection = func() { f.server.invalidatePhoenixAccount(phoenixTestOwner) }
	f.backend.mu.Unlock()
	if multiplier != .8 {
		t.Fatal("projection trusted caller's fee multiplier")
	}
	if _, err := feed.ProjectOpen(context.Background(), order); err == nil {
		t.Fatal("projection survived a known authority mutation")
	}
	if _, err := feed.SubmitSigned(context.Background(), domain.SignedAction{}, nil); !errors.Is(err, errPhoenixLiveDisabled) {
		t.Fatal("projection enabled execution")
	}
}
