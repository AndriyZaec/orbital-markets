package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
)

type prepareStageWriter struct {
	http.ResponseWriter
	onStage func(string)
}

func (w *prepareStageWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *prepareStageWriter) Write(body []byte) (int, error) {
	var event struct{ Type, Stage string }
	if json.Unmarshal(body, &event) == nil && event.Type == "progress" {
		w.onStage(event.Stage)
	}
	return w.ResponseWriter.Write(body)
}

func agePrepareBook(f *localPrepareFixture) {
	market := f.markets[0]
	market.mu.Lock()
	defer market.mu.Unlock()
	market.data.Health.Book.SourceTime = time.Now().Add(-6 * time.Second)
}

func TestPrepareStreamsBBOWaitThenUsesFreshQuotes(t *testing.T) {
	for _, finalCheck := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale_preview", true: "final_liquidity"}[finalCheck], func(t *testing.T) {
			f := newLocalPrepareFixture(t)
			// Optional/unconfigured L2 retains bounded cached-feed waiting.
			f.s.scanner.EnableExecutionLiquidity(nil)
			if !finalCheck {
				agePrepareBook(f)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f.s.handleLivePrepare(&prepareStageWriter{ResponseWriter: w, onStage: func(stage string) {
					if finalCheck && stage == "accounts" {
						agePrepareBook(f)
					}
				}}, r)
			}))
			defer server.Close()
			request, err := http.NewRequest(http.MethodPost, server.URL, jsonBody(t, f.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Accept", "application/x-ndjson")
			client := server.Client()
			client.Timeout = 3 * time.Second
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			decoder := json.NewDecoder(response.Body)
			var event struct {
				Type, Stage string
				Status      int
				Body        json.RawMessage
			}
			for {
				if err := decoder.Decode(&event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "result" {
					t.Fatalf("prepare finished instead of waiting: %s", event.Body)
				}
				if event.Stage == "waiting_bbo" {
					break
				}
			}
			// While the modal waits there are neither signing requests nor a session.
			_, err = f.s.liveStore.ActiveDurableSessionForBindings(context.Background(), f.body["accounts"].(map[string]string), "BTC")
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("session created before BBO was ready: %v", err)
			}
			f.freshen()
			for {
				if err := decoder.Decode(&event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "result" {
					break
				}
			}
			var prepared localPreparedResult
			if err := json.Unmarshal(event.Body, &prepared); err != nil {
				t.Fatal(err)
			}
			if event.Status != http.StatusOK || prepared.SessionID == "" || len(prepared.SigningRequests) != 4 {
				t.Fatalf("fresh BBO did not complete prepare: %d %s", event.Status, event.Body)
			}
			if f.depth.calls != 0 {
				t.Fatalf("BBO wait fetched L2 %d times", f.depth.calls)
			}
		})
	}
}

func TestPrepareBBOTimeoutCreatesNoSessionAndCanRetry(t *testing.T) {
	f := newLocalPrepareFixture(t)
	f.s.scanner.EnableExecutionLiquidity(nil)
	agePrepareBook(f)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	status, _, body, _ := f.prepare(t, ctx, true)
	if status != http.StatusGatewayTimeout {
		t.Fatalf("timeout=%d %s", status, body)
	}
	_, err := f.s.liveStore.ActiveDurableSessionForBindings(context.Background(), f.body["accounts"].(map[string]string), "BTC")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("timeout created a session: %v", err)
	}
	f.freshen()
	status, _, body, _ = f.prepare(t, context.Background(), true)
	if status != http.StatusOK {
		t.Fatalf("retry=%d %s", status, body)
	}
}

func TestPrepareBBORecoveryStillRejectsChangedFunding(t *testing.T) {
	f := newLocalPrepareFixture(t)
	f.s.scanner.EnableExecutionLiquidity(nil)
	agePrepareBook(f)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", jsonBody(t, f.body))
	r.Header.Set("Accept", "application/x-ndjson")
	f.s.handleLivePrepare(&prepareStageWriter{ResponseWriter: w, onStage: func(stage string) {
		if stage != "waiting_bbo" {
			return
		}
		f.freshen()
		market := f.markets[0]
		market.mu.Lock()
		market.data.FundingRate = 0.003
		market.mu.Unlock()
	}}, r)
	if !strings.Contains(w.Body.String(), `"status":422`) || !strings.Contains(w.Body.String(), "funding direction changed") {
		t.Fatalf("changed direction not rejected: %s", w.Body.String())
	}
}

func TestPrepareBBORecoveryPreservesApprovedPriceBounds(t *testing.T) {
	f := newLocalPrepareFixture(t)
	f.s.scanner.EnableExecutionLiquidity(nil)
	f.body["approved_price_limits"] = map[string]float64{"pacifica": 100.5, "hyperliquid": 99.5}
	agePrepareBook(f)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", jsonBody(t, f.body))
	r.Header.Set("Accept", "application/x-ndjson")
	f.s.handleLivePrepare(&prepareStageWriter{ResponseWriter: w, onStage: func(stage string) {
		if stage != "waiting_bbo" {
			return
		}
		f.freshen()
		market := f.markets[0]
		market.mu.Lock()
		market.data.BidPrice, market.data.AskPrice = 102, 102
		market.mu.Unlock()
	}}, r)
	if !strings.Contains(w.Body.String(), `"status":422`) || !strings.Contains(w.Body.String(), "PRICE_MOVED") {
		t.Fatalf("BBO recovery widened approved price bounds: %s", w.Body.String())
	}
	_, err := f.s.liveStore.ActiveDurableSessionForBindings(context.Background(), f.body["accounts"].(map[string]string), "BTC")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("price rejection created a session: %v", err)
	}
}

func TestPrepareBBOWaitHonorsCancellation(t *testing.T) {
	f := newLocalPrepareFixture(t)
	f.s.scanner.EnableExecutionLiquidity(nil)
	agePrepareBook(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", jsonBody(t, f.body)).WithContext(ctx)
	r.Header.Set("Accept", "application/x-ndjson")
	f.s.handleLivePrepare(&prepareStageWriter{ResponseWriter: w, onStage: func(stage string) {
		if stage == "waiting_bbo" {
			cancel()
		}
	}}, r)
	if !strings.Contains(w.Body.String(), "context canceled") {
		t.Fatalf("BBO wait ignored cancellation: %s", w.Body.String())
	}
	_, err := f.s.liveStore.ActiveDurableSessionForBindings(context.Background(), f.body["accounts"].(map[string]string), "BTC")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cancellation created a session: %v", err)
	}
}

func TestPreparePendingBBOUsesOneFreshL2QuoteWithinPreviewLimits(t *testing.T) {
	for _, finalCheck := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale_preview", true: "ages_during_accounts"}[finalCheck], func(t *testing.T) {
			f := newLocalPrepareFixture(t)
			if !finalCheck {
				agePrepareBook(f)
			}
			preview, err := f.s.scanner.BuildPlan(context.Background(), f.body["opportunity_id"].(string), 2, 90)
			if err != nil || !preview.Executable {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			limits := map[string]float64{preview.Leg1.Venue: preview.Leg1.LimitPrice, preview.Leg2.Venue: preview.Leg2.LimitPrice}
			f.body["approved_price_limits"] = limits
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", jsonBody(t, f.body))
			r.Header.Set("Accept", "application/x-ndjson")
			f.s.handleLivePrepare(&prepareStageWriter{ResponseWriter: w, onStage: func(stage string) {
				if finalCheck && stage == "accounts" {
					agePrepareBook(f)
				}
			}}, r)
			if !strings.Contains(w.Body.String(), `"status":200`) || strings.Contains(w.Body.String(), `"stage":"waiting_bbo"`) {
				t.Fatalf("fresh L2 did not complete prepare directly: %s", w.Body.String())
			}
			if f.depth.calls != 1 || f.depth.freshChecks != 1 {
				t.Fatalf("pending preview/prepare polled L2: calls=%d fresh=%d", f.depth.calls, f.depth.freshChecks)
			}
			durable, err := f.s.liveStore.ActiveDurableSessionForBindings(context.Background(), f.body["accounts"].(map[string]string), "BTC")
			if err != nil {
				t.Fatal(err)
			}
			session, found, claimed := f.s.live.sessions.claimForAccounts(durable.ID, f.body["accounts"].(map[string]string))
			if !found || !claimed {
				t.Fatal("prepared session not available")
			}
			defer f.s.live.sessions.release(durable.ID)
			if session.Plan.Liquidity.Status != "l2" {
				t.Fatal("pending liquidity reached signing")
			}
			for _, leg := range []domain.Leg{session.Plan.Leg1, session.Plan.Leg2} {
				if leg.Side == domain.SideLong && leg.LimitPrice > limits[leg.Venue] || leg.Side == domain.SideShort && leg.LimitPrice < limits[leg.Venue] {
					t.Fatal("L2 fallback widened preview approval")
				}
			}
		})
	}
}

func TestPreparePendingBBORejectsFailedOrUnapprovedL2WithoutPolling(t *testing.T) {
	for _, scenario := range []string{"unavailable_or_quota_exhausted", "insufficient", "price_moved", "unacknowledged_impact", "funding_changed", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			f := newLocalPrepareFixture(t)
			agePrepareBook(f)
			f.body["approved_price_limits"] = map[string]float64{"pacifica": 100.5, "hyperliquid": 99.5}
			ctx := context.Background()
			wantStatus, wantText, wantCalls := http.StatusUnprocessableEntity, "depth verification unavailable", 1
			switch scenario {
			case "unavailable_or_quota_exhausted":
				f.depth.err = liquidity.ErrUnavailable
			case "insufficient":
				f.depth.quote.Sufficient = false
				wantText = "insufficient visible"
			case "price_moved":
				f.depth.quote.LongPrice, f.depth.quote.LongWorstPrice = 102, 102
				wantText = "PRICE_MOVED"
			case "unacknowledged_impact":
				f.depth.quote.LongImpact = 0.003
				wantText = "acknowledge elevated price impact"
			case "funding_changed":
				f.markets[0].mu.Lock()
				f.markets[0].data.FundingRate = 0.003
				f.markets[0].mu.Unlock()
				wantText, wantCalls = "funding direction changed", 0
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 40*time.Millisecond)
				defer cancel()
				f.depth.delay = time.Second
				wantStatus, wantText = http.StatusGatewayTimeout, "PREPARE_TIMEOUT"
			}
			status, _, body, _ := f.prepare(t, ctx, true)
			if status != wantStatus || !strings.Contains(body, wantText) {
				t.Fatalf("failure=%d %s, want %d %s", status, body, wantStatus, wantText)
			}
			if f.depth.calls != wantCalls || f.depth.freshChecks != wantCalls {
				t.Fatalf("failed fallback polled L2: calls=%d fresh=%d", f.depth.calls, f.depth.freshChecks)
			}
			_, err := f.s.liveStore.ActiveDurableSessionForBindings(context.Background(), f.body["accounts"].(map[string]string), "BTC")
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("failed fallback created a session: %v", err)
			}
		})
	}
}
