package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster"
)

// Scripts live at the venue boundary. Real handlers, builders, signing-request
// consumption, admission, durable sessions, and reconciliation remain in use.
type hedgeOutcome struct {
	ratio       float64
	rejected    bool
	submitErr   error
	fillErrOnce error
}

type hedgeMatrixFeed struct {
	mu         sync.Mutex
	snapshot   liveAccountSnapshot
	position   float64
	open       []hedgeOutcome
	unwind     hedgeOutcome
	submitted  []domain.SigningRequest
	fills      map[string]normFill
	fillErrors map[string]error
}

func (f *hedgeMatrixFeed) Snapshot() liveAccountSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot
}
func (*hedgeMatrixFeed) PreTradeBlockers(domain.Leg) []string { return nil }
func (f *hedgeMatrixFeed) RefreshPositions(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot.PositionsUpdatedAt = time.Now()
	return nil
}
func (f *hedgeMatrixFeed) WaitForLeverage(_ context.Context, symbol string, leverage float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapshot.LeverageBySymbol[symbol] != leverage {
		return fmt.Errorf("selected leverage not observed")
	}
	return nil
}
func (*hedgeMatrixFeed) LeverageCapability(_ context.Context, _ string, notional float64, _ bool) domain.LeverageCapability {
	maximum := 10
	return domain.LeverageCapability{Status: domain.LeverageCapabilityKnown, Maximum: &maximum, RequestedNotional: notional}
}
func (f *hedgeMatrixFeed) SubmitSigned(ctx context.Context, _ domain.SignedAction, req *domain.SigningRequest) (*domain.SubmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, *req)
	if req.Action == "update_leverage" {
		f.snapshot.LeverageBySymbol = map[string]float64{req.Symbol: float64(req.Leverage)}
		return &domain.SubmissionResult{Accepted: true}, nil
	}
	outcome := f.unwind
	if req.Action == "open" {
		outcome = hedgeOutcome{ratio: 1}
		if len(f.open) > 0 {
			outcome, f.open = f.open[0], f.open[1:]
		}
	}
	if outcome.rejected {
		return &domain.SubmissionResult{Accepted: false, Error: "scripted venue rejection"}, nil
	}
	amount := req.Amount * outcome.ratio
	direction := 1.0
	if req.Side == "sell" || req.Side == "ask" {
		direction = -1
	}
	if req.ReduceOnly {
		// Model the venue's reduce-only cap, not a rewritten unsigned payload.
		amount = min(amount, math.Abs(f.position))
		if f.position*direction >= 0 {
			amount = 0
		}
	}
	f.position += direction * amount
	f.snapshot.Positions = nil
	if math.Abs(f.position) > 1e-9 {
		side := "long"
		if f.position < 0 {
			side = "short"
		}
		f.snapshot.Positions = []liveAccountPosition{{Symbol: req.Symbol, Side: side, Size: math.Abs(f.position), EntryPrice: 100}}
	}
	f.snapshot.PositionsUpdatedAt = time.Now()
	f.fills[req.ID] = normFill{FilledAmount: amount, AvgFillPrice: 100, Filled: amount > 0, Status: "filled", OrderID: req.ClientOrderID}
	if outcome.fillErrOnce != nil {
		f.fillErrors[req.ID] = outcome.fillErrOnce
	}
	if outcome.submitErr != nil {
		return nil, outcome.submitErr
	}
	return &domain.SubmissionResult{Accepted: true, OrderID: req.ClientOrderID}, nil
}
func (f *hedgeMatrixFeed) WaitForFill(_ context.Context, req *domain.SigningRequest) (*normFill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fillErrors[req.ID]; err != nil {
		delete(f.fillErrors, req.ID)
		return nil, err
	}
	fill, ok := f.fills[req.ID]
	if !ok {
		return nil, fmt.Errorf("no scripted order %s", req.ID)
	}
	return &fill, nil
}
func (f *hedgeMatrixFeed) orders() ([]domain.SigningRequest, float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.SigningRequest(nil), f.submitted...), f.position
}

type hedgeMatrixRules struct{}

func (hedgeMatrixRules) OrderRules(string) (aster.OrderRules, bool) {
	return aster.OrderRules{MinPrice: "0.01", MaxPrice: "1000000", TickSize: "0.01", MinQuantity: "0.001", MaxQuantity: "10000", QuantityStep: "0.001", MinNotional: "5"}, true
}

type hedgeMatrixOrder struct{ long, short, first string }

var hedgeMatrixOrders = []hedgeMatrixOrder{
	{"pacifica", "hyperliquid", "pacifica"}, {"pacifica", "hyperliquid", "hyperliquid"},
	{"pacifica", "aster", "pacifica"}, {"pacifica", "aster", "aster"},
	{"hyperliquid", "aster", "hyperliquid"}, {"hyperliquid", "aster", "aster"},
}

type hedgeMatrixResponse struct {
	SessionID    string                   `json:"session_id"`
	Status       string                   `json:"status"`
	PositionID   string                   `json:"position_id"`
	Requests     []*domain.SigningRequest `json:"signing_requests"`
	UnwindStatus string                   `json:"unwind_status"`
	Leg1Fill     *struct {
		Amount float64 `json:"filled_amount"`
	} `json:"leg1_fill"`
	Leg2Fill *struct {
		Amount float64 `json:"filled_amount"`
	} `json:"leg2_fill"`
	Remaining []struct {
		Venue  string  `json:"venue"`
		Amount float64 `json:"amount"`
	} `json:"remaining_exposure"`
}

type hedgeMatrixFixture struct {
	s        *Server
	accounts map[string]string
	feeds    map[string]*hedgeMatrixFeed
	order    hedgeMatrixOrder
	prepared hedgeMatrixResponse
}

func newHedgeMatrixFixture(t *testing.T, order hedgeMatrixOrder) *hedgeMatrixFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	database, err := appdb.Open(filepath.Join(t.TempDir(), "hedge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	t.Cleanup(cancel)
	store := executor.NewStore(database, database, logger)
	deps := NewLiveDeps(ctx, logger, domain.NewSigningRequestStore(), store, liveAmountTestAssetMap{decimals: 3}, liveAmountTestLotSizes{"BTC": "0.001"}, hedgeMatrixRules{}, nil)
	owners := map[string]string{"pacifica": "11111111111111111111111111111111", "hyperliquid": "0x1111111111111111111111111111111111111111", "aster": "0x3333333333333333333333333333333333333333"}
	agents := map[string]string{"pacifica": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", "hyperliquid": "0x2222222222222222222222222222222222222222", "aster": "0x4444444444444444444444444444444444444444"}
	f := &hedgeMatrixFixture{order: order, accounts: make(map[string]string), feeds: make(map[string]*hedgeMatrixFeed)}
	factories := make(map[string]accountFeedFactory)
	boundAgents := make(map[string]string)
	now := time.Now()
	for _, name := range []string{order.long, order.short} {
		feed := &hedgeMatrixFeed{snapshot: liveAccountSnapshot{Venue: name, Account: owners[name], Connected: true, LastUpdated: now, PositionsUpdatedAt: now, Equity: 100000, Available: 100000}, unwind: hedgeOutcome{ratio: 1}, fills: make(map[string]normFill), fillErrors: make(map[string]error)}
		f.accounts[name], boundAgents[name], f.feeds[name] = owners[name], agents[name], feed
		factories[name] = &fixedAccountFeedFactory{feed: feed}
		if err := deps.recordAgentAuthorization(ctx, name, owners[name], agents[name]); err != nil {
			t.Fatal(err)
		}
	}
	deps.accounts = newAccountFeedRegistry(ctx, factories, accountFeedRegistryConfig{})
	market := func(name string, funding float64) executionQuoteAdapter {
		key := "BTC"
		if name == "aster" {
			key = "BTCUSDT"
		}
		return executionQuoteAdapter{name, venue.MarketData{Venue: name, Asset: "BTC", MarketKey: key, MarkPrice: 100, IndexPrice: 100, BidPrice: 100, AskPrice: 100, BidSize: 100, AskSize: 100, OpenInterest: 100000, MaxLeverage: 10, FundingRate: funding, Timestamp: now}}
	}
	sc := scanner.New(logger, market(order.long, 0.001), market(order.short, 0.002))
	sc.EnableAmountNormalization(deps.modules.NormalizeHedgeAmount)
	quote := liquidity.OrderQuote{Sufficient: true, LongPrice: 100, ShortPrice: 100, LongWorstPrice: 100.1, ShortWorstPrice: 99.9, LongImpact: 0.0015, ShortImpact: 0.0001}
	if order.first == order.short {
		quote.LongImpact, quote.ShortImpact = quote.ShortImpact, quote.LongImpact
	}
	sc.EnableExecutionLiquidity(&localPrepareDepth{quote: quote})
	go sc.Run(ctx, time.Hour)
	deadline := time.Now().Add(time.Second)
	for len(sc.Opportunities()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sc.Opportunities()) == 0 {
		t.Fatal("no matrix opportunity")
	}
	f.s = &Server{ctx: ctx, logger: logger, scanner: sc, live: deps, liveStore: store, recoveryOwner: "hedge-matrix"}
	w := httptest.NewRecorder()
	f.s.handleLivePrepare(w, httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", jsonBody(t, map[string]any{
		"opportunity_id": sc.Opportunities()[0].ID, "leverage": 2, "requested_notional": 1000, "accounts": f.accounts, "agents": boundAgents,
	})))
	if w.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &f.prepared); err != nil {
		t.Fatal(err)
	}
	var summary struct {
		Riskier string `json:"riskier_venue"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil || summary.Riskier != order.first {
		t.Fatalf("wrong order: %s", w.Body.String())
	}
	return f
}

func (f *hedgeMatrixFixture) advance(t *testing.T, requests []*domain.SigningRequest, abort bool) hedgeMatrixResponse {
	t.Helper()
	actions := signHedgeMatrixRequests(requests)
	w := httptest.NewRecorder()
	f.s.handleLiveAdvance(w, httptest.NewRequest(http.MethodPost, "/api/v1/live/advance", jsonBody(t, map[string]any{
		"session_id": f.prepared.SessionID, "accounts": f.accounts, "signed_actions": actions, "abort": abort,
	})))
	if w.Code != http.StatusOK {
		t.Fatalf("advance: %d %s", w.Code, w.Body.String())
	}
	var response hedgeMatrixResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func signHedgeMatrixRequests(requests []*domain.SigningRequest) []domain.SignedAction {
	actions := make([]domain.SignedAction, 0, len(requests))
	for _, req := range requests {
		actions = append(actions, domain.SignedAction{RequestID: req.ID, ClientOrderID: req.ClientOrderID, Venue: req.Venue, SignerAddress: req.Signer, Signature: "fixture-signature"})
	}
	return actions
}

func (f *hedgeMatrixFixture) status(t *testing.T) hedgeMatrixResponse {
	t.Helper()
	query := make(url.Values)
	for venueName, account := range f.accounts {
		query.Set("accounts["+venueName+"]", account)
	}
	w := httptest.NewRecorder()
	f.s.handleLiveSessionStatus(w, httptest.NewRequest(http.MethodGet, "/api/v1/live/sessions/"+f.prepared.SessionID+"?"+query.Encode(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	var response hedgeMatrixResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestHedgeMatrixFullOpen(t *testing.T) {
	for _, order := range hedgeMatrixOrders {
		t.Run(order.long+"_"+order.short+"_first_"+order.first, func(t *testing.T) {
			f := newHedgeMatrixFixture(t, order)
			leg1 := f.advance(t, f.prepared.Requests, false)
			if leg1.Status != "awaiting_leg2_sign" || len(leg1.Requests) != 1 || leg1.Requests[0].Amount != 10 {
				t.Fatalf("leg1 result=%+v", leg1)
			}
			leg2 := f.advance(t, leg1.Requests, false)
			if leg2.Status != "open" || leg2.PositionID == "" {
				t.Fatalf("hedge result=%+v", leg2)
			}
			if persisted := f.status(t); persisted.Status != "open" || persisted.PositionID != leg2.PositionID {
				t.Fatalf("persisted=%+v", persisted)
			}
			for name, feed := range f.feeds {
				requests, position := feed.orders()
				want := 10.0
				if name == order.short {
					want = -10
				}
				if position != want {
					t.Fatalf("%s position=%g want=%g", name, position, want)
				}
				opens := 0
				for _, req := range requests {
					if req.Action == "open" {
						opens++
					}
					if req.ReduceOnly {
						t.Fatal("successful hedge fired unwind")
					}
				}
				if opens != 1 {
					t.Fatalf("%s open count=%d", name, opens)
				}
			}
		})
	}
}

func TestHedgeMatrixInvalidHedgeSignatureUnwindsWithoutSubmission(t *testing.T) {
	for _, order := range hedgeMatrixOrders {
		t.Run(order.long+"_"+order.short+"_first_"+order.first, func(t *testing.T) {
			f := newHedgeMatrixFixture(t, order)
			leg1 := f.advance(t, f.prepared.Requests, false)
			if leg1.Status != "awaiting_leg2_sign" || len(leg1.Requests) != 1 {
				t.Fatalf("leg1=%+v", leg1)
			}
			bad := *leg1.Requests[0]
			bad.Signer = "wrong-agent"
			response := f.advance(t, []*domain.SigningRequest{&bad}, false)
			if response.Status != "aborted" || response.UnwindStatus != "confirmed" {
				t.Fatalf("invalid signature recovery=%+v", response)
			}
			if f.status(t).Status != "aborted" {
				t.Fatal("recovery was not persisted")
			}
			for name, feed := range f.feeds {
				requests, position := feed.orders()
				if position != 0 {
					t.Fatalf("%s left exposed after clean unwind", name)
				}
				if name != order.first {
					for _, request := range requests {
						if request.Action == "open" {
							t.Fatal("invalid hedge signature reached the venue")
						}
					}
				}
			}
		})
	}
}

func TestHedgeMatrixPartialFailureAndRecovery(t *testing.T) {
	unknown := errors.New("scripted response lost")
	for _, scenario := range []struct {
		name                             string
		first                            hedgeOutcome
		second                           []hedgeOutcome
		unwind                           hedgeOutcome
		abort                            bool
		status                           string
		firstPosition, secondPosition    float64
		firstOpens, secondOpens, unwinds int
	}{
		{"partial_first_hedged", hedgeOutcome{ratio: .6}, nil, hedgeOutcome{ratio: 1}, false, "open", 6, 6, 1, 1, 0},
		{"exact_half_first_hedged", hedgeOutcome{ratio: .5}, nil, hedgeOutcome{ratio: 1}, false, "open", 5, 5, 1, 1, 0},
		{"underfilled_first_unwound", hedgeOutcome{ratio: .4}, nil, hedgeOutcome{ratio: 1}, false, "aborted", 0, 0, 1, 0, 1},
		{"first_rejected", hedgeOutcome{rejected: true}, nil, hedgeOutcome{ratio: 1}, false, "failed", 0, 0, 1, 0, 0},
		{"browser_aborts_before_hedge", hedgeOutcome{ratio: 1}, nil, hedgeOutcome{ratio: 1}, true, "aborted", 0, 0, 1, 0, 1},
		{"rejected_hedge_retries_once", hedgeOutcome{ratio: 1}, []hedgeOutcome{{rejected: true}, {ratio: 1}}, hedgeOutcome{ratio: 1}, false, "open", 10, 10, 1, 2, 0},
		{"partial_hedge_retries_residual", hedgeOutcome{ratio: 1}, []hedgeOutcome{{ratio: .4}, {ratio: 1}}, hedgeOutcome{ratio: 1}, false, "open", 10, 10, 1, 2, 0},
		{"hedge_at_five_percent_tolerance", hedgeOutcome{ratio: 1}, []hedgeOutcome{{ratio: .95}}, hedgeOutcome{ratio: 1}, false, "open", 10, 9.5, 1, 1, 0},
		{"failed_retry_exposes_remaining_hedge", hedgeOutcome{ratio: 1}, []hedgeOutcome{{ratio: .4}, {rejected: true}}, hedgeOutcome{ratio: 1}, false, "degraded", 0, 4, 1, 2, 1},
		{"unwind_rejected", hedgeOutcome{ratio: 1}, nil, hedgeOutcome{rejected: true}, true, "degraded", 10, 0, 1, 0, 1},
		{"unwind_unconfirmed", hedgeOutcome{ratio: 1}, nil, hedgeOutcome{ratio: 0, fillErrOnce: unknown}, true, "degraded", 10, 0, 1, 0, 1},
		{"lost_first_submit_reconciles_then_unwinds", hedgeOutcome{ratio: 1, submitErr: unknown}, nil, hedgeOutcome{ratio: 1}, false, "aborted", 0, 0, 1, 0, 1},
		{"lost_hedge_submit_reconciles_without_retry", hedgeOutcome{ratio: 1}, []hedgeOutcome{{ratio: 1, submitErr: unknown}}, hedgeOutcome{ratio: 1}, false, "open", 10, 10, 1, 1, 0},
		{"lost_hedge_fill_reconciles_without_retry", hedgeOutcome{ratio: 1}, []hedgeOutcome{{ratio: 1, fillErrOnce: unknown}}, hedgeOutcome{ratio: 1}, false, "open", 10, 10, 1, 1, 0},
	} {
		for _, order := range hedgeMatrixOrders {
			t.Run(scenario.name+"/"+order.long+"_"+order.short+"_first_"+order.first, func(t *testing.T) {
				f := newHedgeMatrixFixture(t, order)
				secondVenue := order.short
				if order.first == order.short {
					secondVenue = order.long
				}
				f.feeds[order.first].open, f.feeds[order.first].unwind = []hedgeOutcome{scenario.first}, scenario.unwind
				f.feeds[secondVenue].open = scenario.second
				response := f.advance(t, f.prepared.Requests, false)
				if response.Status == "awaiting_leg2_sign" {
					if len(response.Requests) != 1 || response.Requests[0].Amount != 10*scenario.first.ratio {
						t.Fatalf("hedge did not use confirmed fill: %+v", response)
					}
					response = f.advance(t, response.Requests, scenario.abort)
				}
				if response.Status == "awaiting_leg2_retry_sign" {
					if len(response.Requests) != 1 {
						t.Fatalf("retry=%+v", response)
					}
					wanted := 10.0
					if scenario.second[0].ratio > 0 {
						wanted = 6
					}
					if response.Requests[0].Amount != wanted {
						t.Fatalf("retry amount=%g want=%g", response.Requests[0].Amount, wanted)
					}
					response = f.advance(t, response.Requests, false)
				}
				if response.Status == "recovering" {
					deadline := time.Now().Add(5 * time.Second)
					for response.Status == "recovering" && time.Now().Before(deadline) {
						time.Sleep(20 * time.Millisecond)
						response = f.status(t)
					}
				}
				if response.Status != scenario.status {
					t.Fatalf("status=%s want=%s", response.Status, scenario.status)
				}
				persisted := f.status(t)
				if persisted.Status != scenario.status {
					t.Fatalf("persisted status=%s want=%s", persisted.Status, scenario.status)
				}
				if scenario.status == "open" && (persisted.Leg1Fill == nil || persisted.Leg2Fill == nil ||
					persisted.Leg1Fill.Amount != scenario.firstPosition || persisted.Leg2Fill.Amount != scenario.secondPosition) {
					t.Fatalf("persisted fills do not match confirmed exposure: %+v", persisted)
				}
				for _, leg := range []struct {
					venueName string
					position  float64
					opens     int
				}{
					{order.first, scenario.firstPosition, scenario.firstOpens}, {secondVenue, scenario.secondPosition, scenario.secondOpens},
				} {
					requests, position := f.feeds[leg.venueName].orders()
					wanted := leg.position
					if leg.venueName == order.short {
						wanted = -wanted
					}
					if math.Abs(position-wanted) > 1e-9 {
						t.Fatalf("%s position=%g want=%g", leg.venueName, position, wanted)
					}
					opens, unwinds := 0, 0
					seen := make(map[string]bool)
					for _, request := range requests {
						if seen[request.ID] {
							t.Fatal("duplicate venue submission")
						}
						seen[request.ID] = true
						if request.Action == "open" {
							opens++
						}
						if request.Action == "unwind" {
							unwinds++
							if !request.ReduceOnly || request.Venue != order.first {
								t.Fatal("unwind is not reduce-only on the exposed first venue")
							}
							matched := false
							for _, prepared := range f.prepared.Requests {
								if prepared.ID == request.ID && prepared.Action == "unwind" && prepared.Amount == request.Amount && bytes.Equal(prepared.UnsignedPayload, request.UnsignedPayload) {
									matched = true
								}
							}
							if !matched {
								t.Fatal("recovery changed the pre-signed unwind payload")
							}
						}
					}
					if opens != leg.opens {
						t.Fatalf("%s opens=%d want=%d", leg.venueName, opens, leg.opens)
					}
					wantedUnwinds := 0
					if leg.venueName == order.first {
						wantedUnwinds = scenario.unwinds
					}
					if unwinds != wantedUnwinds {
						t.Fatalf("%s unwinds=%d want=%d", leg.venueName, unwinds, wantedUnwinds)
					}
					if scenario.status == "degraded" && leg.position > 0 {
						found := false
						for _, exposure := range persisted.Remaining {
							if exposure.Venue == leg.venueName && exposure.Amount == leg.position {
								found = true
							}
						}
						if !found {
							t.Fatalf("residual %s=%g hidden: %+v", leg.venueName, leg.position, persisted.Remaining)
						}
					}
				}
				// Replay a previously consumed batch after terminal persistence.
				before := make(map[string]int)
				for name, feed := range f.feeds {
					requests, _ := feed.orders()
					before[name] = len(requests)
				}
				w := httptest.NewRecorder()
				f.s.handleLiveAdvance(w, httptest.NewRequest(http.MethodPost, "/api/v1/live/advance", jsonBody(t, map[string]any{
					"session_id": f.prepared.SessionID, "accounts": f.accounts, "signed_actions": signHedgeMatrixRequests(f.prepared.Requests),
				})))
				if w.Code == http.StatusOK {
					t.Fatal("terminal session accepted another advance")
				}
				for name, feed := range f.feeds {
					requests, _ := feed.orders()
					if len(requests) != before[name] {
						t.Fatal("terminal replay submitted another order")
					}
				}
			})
		}
	}
}
