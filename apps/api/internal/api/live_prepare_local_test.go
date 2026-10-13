package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type localPrepareFeed struct {
	*fakeAccountFeed
	submissions atomic.Int64
}

func (f *localPrepareFeed) SubmitSigned(ctx context.Context, action domain.SignedAction, request *domain.SigningRequest) (*domain.SubmissionResult, error) {
	f.submissions.Add(1)
	return f.fakeAccountFeed.SubmitSigned(ctx, action, request)
}

type localPrepareDepth struct {
	calls       int
	freshChecks int
	delay       time.Duration
	quote       liquidity.OrderQuote
}

func (d *localPrepareDepth) Quote(ctx context.Context, _ liquidity.Pair, size liquidity.OrderSize, fresh bool) (liquidity.OrderQuote, error) {
	d.calls++
	if fresh {
		d.freshChecks++
	}
	if d.delay > 0 {
		select {
		case <-ctx.Done():
			return liquidity.OrderQuote{}, ctx.Err()
		case <-time.After(d.delay):
		}
	}
	quote := d.quote
	quote.BaseAmount, quote.ObservedAt = size.BaseAmount, time.Now()
	return quote, nil
}

type localPrepareFixture struct {
	s       *Server
	markets []*prepareMarketAdapter
	feeds   map[string]*localPrepareFeed
	depth   *localPrepareDepth
	body    map[string]any
}

type localPreparedResult struct {
	SessionID       string                   `json:"session_id"`
	RiskierVenue    string                   `json:"riskier_venue"`
	SigningRequests []*domain.SigningRequest `json:"signing_requests"`
}

func newLocalPrepareFixture(t *testing.T) *localPrepareFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	database, err := appdb.Open(filepath.Join(t.TempDir(), "prepare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	store := executor.NewStore(database, database, logger)
	deps := NewLiveDeps(ctx, logger, domain.NewSigningRequestStore(), store,
		liveAmountTestAssetMap{decimals: 3}, liveAmountTestLotSizes{"BTC": "0.001"}, nil, nil)
	accounts := map[string]string{"pacifica": "11111111111111111111111111111111", "hyperliquid": "0x1111111111111111111111111111111111111111"}
	agents := map[string]string{"pacifica": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", "hyperliquid": "0x2222222222222222222222222222222222222222"}
	f := &localPrepareFixture{feeds: make(map[string]*localPrepareFeed)}
	factories := make(map[string]accountFeedFactory)
	for name, account := range accounts {
		feed := &localPrepareFeed{fakeAccountFeed: &fakeAccountFeed{snapshot: liveAccountSnapshot{
			Venue: name, Account: account, Connected: true, Equity: 100000, Available: 100000,
		}}}
		f.feeds[name], factories[name] = feed, &fixedAccountFeedFactory{feed: feed}
		if err := deps.recordAgentAuthorization(ctx, name, account, agents[name]); err != nil {
			t.Fatal(err)
		}
	}
	deps.accounts = newAccountFeedRegistry(ctx, factories, accountFeedRegistryConfig{})
	market := func(name string, funding float64) *prepareMarketAdapter {
		return &prepareMarketAdapter{data: venue.MarketData{
			Venue: name, Asset: "BTC", MarketKey: "BTC", MarkPrice: 100, IndexPrice: 100,
			BidPrice: 100, AskPrice: 100, BidSize: 100, AskSize: 100, OpenInterest: 100000,
			MaxLeverage: 10, FundingRate: funding,
		}}
	}
	f.markets = []*prepareMarketAdapter{market("pacifica", 0.001), market("hyperliquid", 0.002)}
	f.depth = &localPrepareDepth{quote: liquidity.OrderQuote{
		Sufficient: true, LongPrice: 100, ShortPrice: 100,
		LongWorstPrice: 100.1, ShortWorstPrice: 99.9, LongImpact: 0.001, ShortImpact: 0.001,
	}}
	f.freshen()
	sc := scanner.New(logger, f.markets[0], f.markets[1])
	sc.EnableAmountNormalization(deps.modules.NormalizeHedgeAmount)
	sc.EnableExecutionLiquidity(f.depth)
	go sc.Run(ctx, time.Hour)
	deadline := time.Now().Add(time.Second)
	for len(sc.Opportunities()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sc.Opportunities()) == 0 {
		t.Fatal("no fixture opportunity")
	}
	f.s = &Server{ctx: ctx, logger: logger, scanner: sc, live: deps, liveStore: store, recoveryOwner: "local-prepare-test"}
	f.body = map[string]any{"opportunity_id": sc.Opportunities()[0].ID, "leverage": 2,
		"requested_notional": 90, "accounts": accounts, "agents": agents}
	t.Cleanup(func() {
		for name, feed := range f.feeds {
			if count := feed.submissions.Load(); count != 0 {
				t.Errorf("prepare submitted %d orders to %s", count, name)
			}
		}
	})
	return f
}

func (f *localPrepareFixture) freshen() {
	now := time.Now()
	for _, market := range f.markets {
		market.mu.Lock()
		market.data.Timestamp = now
		market.data.Health = &venue.MarketHealth{
			Index: venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now},
			Book:  venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now},
		}
		market.mu.Unlock()
	}
	for _, feed := range f.feeds {
		feed.mu.Lock()
		feed.snapshot.LastUpdated, feed.snapshot.PositionsUpdatedAt = now, now
		feed.mu.Unlock()
	}
}

func (f *localPrepareFixture) prepare(t *testing.T, ctx context.Context, stream bool) (int, localPreparedResult, string, time.Duration) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", jsonBody(t, f.body)).WithContext(ctx)
	if stream {
		r.Header.Set("Accept", "application/x-ndjson")
	}
	start := time.Now()
	f.s.handleLivePrepare(w, r)
	elapsed := time.Since(start)
	status, body := w.Code, w.Body.Bytes()
	if stream {
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		for {
			var event struct {
				Type   string
				Status int
				Body   json.RawMessage
			}
			if err := decoder.Decode(&event); err != nil {
				t.Fatalf("stream ended without result: %v %s", err, body)
			}
			if event.Type == "result" {
				status, body = event.Status, event.Body
				break
			}
		}
	}
	var result localPreparedResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if status == http.StatusOK {
		if result.SessionID == "" || len(result.SigningRequests) != 4 {
			t.Fatalf("incomplete signing result: %s", body)
		}
		durable, err := f.s.liveStore.ActiveDurableSessionForBindings(ctx, f.body["accounts"].(map[string]string), "BTC")
		if err != nil || durable.ID != result.SessionID {
			t.Fatalf("returned session not durable: %+v %v", durable, err)
		}
	}
	return status, result, string(body), elapsed
}

func TestLocalPrepareWarmBBOAndSafeRetry(t *testing.T) {
	f := newLocalPrepareFixture(t)
	var durations []time.Duration
	var previous string
	for i := range 100 {
		f.freshen()
		status, result, body, duration := f.prepare(t, context.Background(), i%2 == 0)
		if status != http.StatusOK {
			t.Fatalf("prepare %d: %d %s", i, status, body)
		}
		if result.SessionID == previous {
			t.Fatal("safe retry reused a previous unsigned session")
		}
		previous = result.SessionID
		durations = append(durations, duration)
	}
	if f.depth.calls != 0 {
		t.Fatalf("BBO-covered opens made %d L2 reads", f.depth.calls)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("warm cached BBO prepare, n=100 (JSON/stream alternating, real builders+SQLite): p50=%s p95=%s max=%s; L2 calls=0, order submissions=0", durations[49], durations[94], durations[99])
}

func TestLocalPrepareColdAccountsAndTimeoutCanRetry(t *testing.T) {
	f := newLocalPrepareFixture(t)
	feed := f.feeds["pacifica"]
	feed.mu.Lock()
	feed.snapshot.LastUpdated = time.Time{}
	feed.mu.Unlock()
	status, _, body, duration := f.prepare(t, context.Background(), true)
	if status != http.StatusConflict || !strings.Contains(body, livePrepareAccountStateNotReady) {
		t.Fatalf("cold account: %d %s", status, body)
	}
	t.Logf("cold account readiness rejection: %s", duration)
	f.freshen()
	accounts, err := f.s.live.acquireAccountContext(f.body["accounts"].(map[string]string), false)
	if err != nil {
		t.Fatal(err)
	}
	unlock := accounts.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	status, _, body, duration = f.prepare(t, ctx, true)
	cancel()
	unlock()
	accounts.Release()
	if status != http.StatusGatewayTimeout || !strings.Contains(body, "PREPARE_TIMEOUT") {
		t.Fatalf("lock timeout: %d %s", status, body)
	}
	t.Logf("busy account deadline (configured20ms): %s", duration)
	status, _, body, _ = f.prepare(t, context.Background(), true)
	if status != http.StatusOK {
		t.Fatalf("retry after timeout blocked: %d %s", status, body)
	}
}

func TestLocalPrepareL2AckAndUnavailableDepth(t *testing.T) {
	f := newLocalPrepareFixture(t)
	f.body["requested_notional"] = 500
	f.depth.delay = 20 * time.Millisecond
	status, result, body, duration := f.prepare(t, context.Background(), true)
	if status != http.StatusOK {
		t.Fatalf("L2 prepare: %d %s", status, body)
	}
	if f.depth.calls != 2 || f.depth.freshChecks != 1 {
		t.Fatalf("L2 reads=%d fresh=%d", f.depth.calls, f.depth.freshChecks)
	}
	t.Logf("L2 prepare (injected20ms per pair quote): %s; quotes=%d fresh=%d; requests=%d", duration, f.depth.calls, f.depth.freshChecks, len(result.SigningRequests))
	f.depth.delay = 0
	f.depth.quote.LongImpact, f.depth.quote.ShortImpact = 0.003, 0.003
	status, _, body, _ = f.prepare(t, context.Background(), true)
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "acknowledge elevated price impact") {
		t.Fatalf("unacknowledged impact: %d %s", status, body)
	}
	f.body["impact_acknowledged"] = true
	status, _, body, _ = f.prepare(t, context.Background(), true)
	if status != http.StatusOK {
		t.Fatalf("acknowledged impact: %d %s", status, body)
	}
	f.depth.quote.Sufficient = false
	status, _, body, _ = f.prepare(t, context.Background(), true)
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "insufficient visible") {
		t.Fatalf("insufficient depth: %d %s", status, body)
	}
}

func TestPrepareKeepsFreshBBOInsideApprovedLimitsWithoutExtraDepthReads(t *testing.T) {
	for _, price := range []float64{100.4, 99.6, 100.5, 99.5, 101, 99} {
		t.Run(fmt.Sprintf("price_%g", price), func(t *testing.T) {
			f := newLocalPrepareFixture(t)
			f.body["approved_price_limits"] = map[string]float64{"pacifica": 100.5, "hyperliquid": 99.5}
			for _, market := range f.markets {
				market.mu.Lock()
				market.data.MarkPrice, market.data.IndexPrice = price, price
				market.data.AskPrice, market.data.BidPrice = price, price
				market.mu.Unlock()
			}
			status, result, body, _ := f.prepare(t, context.Background(), true)
			if price > 100.5 || price < 99.5 {
				if status != http.StatusUnprocessableEntity || !strings.Contains(body, "PRICE_MOVED") {
					t.Fatalf("outside approval: %d %s", status, body)
				}
			} else {
				if status != http.StatusOK {
					t.Fatalf("inside approval blocked: %d %s", status, body)
				}
				session, found, claimed := f.s.live.sessions.claimForAccounts(result.SessionID, f.body["accounts"].(map[string]string))
				if !found || !claimed {
					t.Fatal("session not found")
				}
				defer f.s.live.sessions.release(result.SessionID)
				if session.Plan.Leg1.LimitPrice > 100.5 || session.Plan.Leg2.LimitPrice < 99.5 {
					t.Fatal("prepare widened approval")
				}
			}
			if f.depth.calls != 0 {
				t.Fatal("clamping a BBO quote added L2 reads")
			}
		})
	}
}
