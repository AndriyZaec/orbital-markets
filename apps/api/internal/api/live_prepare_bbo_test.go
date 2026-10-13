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
		t.Run(map[bool]string{false: "initial_plan", true: "final_liquidity"}[finalCheck], func(t *testing.T) {
			f := newLocalPrepareFixture(t)
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
