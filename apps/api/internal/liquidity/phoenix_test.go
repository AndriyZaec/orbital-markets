package liquidity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestPhoenixDepthQuotesBothLegsAndRefreshes(t *testing.T) {
	var requests atomic.Int32
	var unavailable atomic.Bool
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/view/orderbook/BTC" {
			serveTestBook(w, r)
			return
		}
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Query().Get("include_splines") != "true" || r.URL.Query().Get("bypass_execution_band") != "false" {
			t.Errorf("unexpected Phoenix request: %s %s", r.Method, r.URL)
		}
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"slot":462087144,"symbol":"BTC","bids":[[99,1],[98,2]],"asks":[[100,1],[101,2]]}`)
	})
	if requests.Load() != 0 || len(s.books) != 0 {
		t.Fatal("service started background collection")
	}
	phoenix, hl := Market{"phoenix", "BTC"}, Market{"hyperliquid", "BTC"}
	for _, pair := range []Pair{{phoenix, hl}, {hl, phoenix}} {
		quote, err := s.Quote(context.Background(), pair, OrderSize{BaseAmount: 2}, false)
		if err != nil || !quote.Sufficient {
			t.Fatalf("pair=%+v quote=%+v err=%v", pair, quote, err)
		}
		if pair.Long == phoenix {
			if math.Abs(quote.LongImpact-0.005) > 1e-12 || quote.LongWorstPrice != 101 {
				t.Fatalf("Phoenix ask depth was not base-sized: %+v", quote)
			}
		} else if math.Abs(quote.ShortImpact-0.5/99) > 1e-12 || quote.ShortWorstPrice != 98 {
			t.Fatalf("Phoenix bid depth was not base-sized: %+v", quote)
		}
	}
	pair := Pair{phoenix, hl}
	quote, err := s.Quote(context.Background(), pair, OrderSize{BaseAmount: 4}, false)
	if err != nil || quote.Sufficient || requests.Load() != 1 {
		t.Fatalf("insufficient cached depth: %+v err=%v requests=%d", quote, err, requests.Load())
	}
	unavailable.Store(true)
	if _, err := s.Quote(context.Background(), pair, OrderSize{BaseAmount: 2}, true); err == nil {
		t.Fatal("fresh quote reused cached Phoenix success")
	}
	if requests.Load() != 2 {
		t.Fatalf("fresh Phoenix requests=%d", requests.Load())
	}
}

func TestPhoenixBookValidation(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
	}{
		{"malformed", `{"slot":`},
		{"wrong symbol", `{"slot":1,"symbol":"ETH","bids":[[99,1]],"asks":[[100,1]]}`},
		{"missing slot", `{"symbol":"BTC","bids":[[99,1]],"asks":[[100,1]]}`},
		{"zero slot", `{"slot":0,"symbol":"BTC","bids":[[99,1]],"asks":[[100,1]]}`},
		{"empty side", `{"slot":1,"symbol":"BTC","bids":[],"asks":[[100,1]]}`},
		{"short tuple", `{"slot":1,"symbol":"BTC","bids":[[99]],"asks":[[100,1]]}`},
		{"extra tuple field", `{"slot":1,"symbol":"BTC","bids":[[99,1,2]],"asks":[[100,1]]}`},
		{"negative size", `{"slot":1,"symbol":"BTC","bids":[[99,-1]],"asks":[[100,1]]}`},
		{"null price", `{"slot":1,"symbol":"BTC","bids":[[null,1]],"asks":[[100,1]]}`},
		{"overflow", `{"slot":1,"symbol":"BTC","bids":[[99,1e400]],"asks":[[100,1]]}`},
		{"unsorted", `{"slot":1,"symbol":"BTC","bids":[[98,1],[99,1]],"asks":[[100,1]]}`},
		{"duplicate", `{"slot":1,"symbol":"BTC","bids":[[99,1],[99,1]],"asks":[[100,1]]}`},
		{"crossed", `{"slot":1,"symbol":"BTC","bids":[[101,1]],"asks":[[100,1]]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodePhoenixBook("BTC", []byte(tc.raw), time.Now()); err == nil {
				t.Fatal("invalid Phoenix depth accepted")
			}
		})
	}
}

func TestPhoenixBookBoundsAndObservationFreshness(t *testing.T) {
	bids, asks := make([][]float64, 60), make([][]float64, 60)
	for i := range bids {
		bids[i], asks[i] = []float64{99 - float64(i), 1}, []float64{100 + float64(i), 1}
	}
	raw, err := json.Marshal(map[string]any{"slot": 462087144, "symbol": "BTC", "bids": bids, "asks": asks})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second)
	b, err := decodePhoenixBook("BTC", raw, at)
	if err != nil || len(b.bids) != 50 || len(b.asks) != 50 || b.at != at {
		t.Fatalf("bounded book=%+v err=%v", b, err)
	}
	quote, err := quoteOrder(b, b, 51, time.Now())
	if err != nil || quote.Sufficient {
		t.Fatalf("extrapolated beyond retained depth: %+v err=%v", quote, err)
	}
	if _, err := decodePhoenixBook("BTC", raw, time.Now().Add(-maxBookAge-time.Second)); err == nil {
		t.Fatal("stale observation accepted")
	}
	if _, err := quoteOrder(b, book{bids: b.bids, asks: b.asks, at: at.Add(-maxBookSkew - time.Millisecond)}, 1, time.Now()); err == nil {
		t.Fatal("misaligned pair accepted")
	}
}

func TestPhoenixRateLimitAndBudget(t *testing.T) {
	var requests atomic.Int32
	s := testService(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	for _, key := range []string{"BTC", "ETH"} {
		if _, err := s.fetchBook(context.Background(), Market{"phoenix", key}); err == nil {
			t.Fatal("rate-limited Phoenix accepted")
		}
	}
	if requests.Load() != 1 || time.Until(s.quotas["phoenix"].pausedUntil) < 110*time.Second {
		t.Fatal("Phoenix Retry-After not respected")
	}
	s.quotas["phoenix"].pausedUntil = time.Time{}
	now := time.Now()
	// The rejected HTTP request consumed one local budget entry.
	for range 29 {
		if !s.allow("phoenix", now) {
			t.Fatal("Phoenix budget denied early")
		}
	}
	if s.allow("phoenix", now) || !s.allow("phoenix", now.Add(time.Minute+time.Second)) {
		t.Fatal("Phoenix rolling request budget not enforced")
	}
}

func TestPhoenixRequestTimeout(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	s.client.Timeout = 20 * time.Millisecond
	started := time.Now()
	if _, err := s.fetchBook(context.Background(), Market{"phoenix", "BTC"}); err == nil || time.Since(started) > time.Second {
		t.Fatalf("Phoenix timeout not bounded: %v", err)
	}
}
