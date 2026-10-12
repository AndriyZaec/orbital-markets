package liquidity

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func serveTestBook(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UnixMilli()
	switch r.URL.Path {
	case "/info":
		var request struct{ Type, Coin string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		_, _ = fmt.Fprintf(w, `{"coin":%q,"time":%d,"levels":[[{"px":"99","sz":"5"}],[{"px":"100","sz":"5"}]]}`, request.Coin, now)
	case "/api/v1/book":
		_, _ = fmt.Fprintf(w, `{"success":true,"data":{"s":%q,"t":%d,"l":[[{"p":"99","a":"5"}],[{"p":"100","a":"5"}]]}}`, r.URL.Query().Get("symbol"), now)
	default:
		_, _ = fmt.Fprintf(w, `{"E":%d,"bids":[["99","5"]],"asks":[["100","5"]]}`, now)
	}
}

func testService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	s := New(context.Background(), slog.Default())
	for venue := range s.urls {
		s.urls[venue] = server.URL
	}
	return s
}

func TestConcurrentViewsShareCachedPublicBooks(t *testing.T) {
	var requests atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(20 * time.Millisecond)
		serveTestBook(w, r)
	})
	pair := Pair{Market{"aster", "BTCUSDT"}, Market{"hyperliquid", "BTC"}}
	var workers sync.WaitGroup
	for range 10 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			value, err := s.Estimate(context.Background(), pair)
			if err != nil || value.Notional != 495 {
				t.Errorf("estimate=%+v, err=%v", value, err)
			}
		}()
	}
	workers.Wait()
	if requests.Load() != 2 {
		t.Fatalf("requests=%d, want one per venue", requests.Load())
	}
	// Another pair reuses the same Aster book instead of fetching it again.
	_, err := s.Estimate(context.Background(), Pair{pair.Long, Market{"pacifica", "BTC"}})
	if err != nil || requests.Load() != 3 {
		t.Fatalf("shared book requests=%d, err=%v", requests.Load(), err)
	}
}

func TestRateLimitStopsOptionalReads(t *testing.T) {
	var requests atomic.Int32
	s := testService(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := s.fetchBook(context.Background(), Market{"pacifica", "BTC"}); err == nil {
		t.Fatal("429 accepted")
	}
	if _, err := s.fetchBook(context.Background(), Market{"pacifica", "ETH"}); err == nil {
		t.Fatal("paused venue accepted")
	}
	if requests.Load() != 1 {
		t.Fatal("optional feed retried despite 429")
	}
	if time.Until(s.quotas["pacifica"].pausedUntil) < 110*time.Second {
		t.Fatal("Retry-After ignored")
	}
}

func TestCreditHeadersAndRequestBudget(t *testing.T) {
	s := New(context.Background(), slog.Default())
	header := http.Header{}
	header.Set("ratelimit", `"credits";r=490;t=30`)
	header.Set("ratelimit-policy", `"credits";q=1000;w=60`)
	s.observeLimits("pacifica", &http.Response{StatusCode: 200, Header: header})
	if s.allow("pacifica", time.Now()) {
		t.Fatal("low credit reserve did not pause optional reads")
	}
	s.quotas["pacifica"].pausedUntil = time.Time{}
	now := time.Now()
	for range 30 {
		if !s.allow("pacifica", now) {
			t.Fatal("budget denied early")
		}
	}
	if s.allow("pacifica", now) {
		t.Fatal("Pacifica budget exceeded")
	}
	if !s.allow("pacifica", now.Add(time.Minute+time.Nanosecond)) {
		t.Fatal("rolling budget did not recover")
	}
}

func TestInvalidDataAndTimeoutRemainOptional(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"coin":"BTC","time":1,"levels":[[],[]]}`)
	})
	pair := Pair{Market{"aster", "BTCUSDT"}, Market{"hyperliquid", "BTC"}}
	if _, err := s.Estimate(context.Background(), pair); err == nil {
		t.Fatal("invalid books accepted")
	}
	if _, err := decodeRESTBook("pacifica", "BTC", []byte(`{"success":true,"data":{"s":"BTC","t":1,"l":[[],[{"p":"100","a":"NaN"}]]}}`)); err == nil {
		t.Fatal("NaN accepted")
	}
	blocked := testService(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
			serveTestBook(w, r)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := blocked.Estimate(ctx, pair); err != context.Canceled {
		t.Fatalf("cancel error=%v", err)
	}
	if len(blocked.pairs) != 0 {
		t.Fatal("cancelled view started network work")
	}
	blocked.client.Timeout = 10 * time.Millisecond
	started := time.Now()
	if _, err := blocked.Estimate(context.Background(), pair); err == nil || time.Since(started) > time.Second {
		t.Fatalf("slow venue request was not bounded: err=%v", err)
	}
}

func TestClosingOneViewDoesNotCancelAnotherSharedQuote(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var requests atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		started <- struct{}{}
		<-release
		serveTestBook(w, r)
	})
	pair := Pair{Market{"aster", "BTCUSDT"}, Market{"hyperliquid", "BTC"}}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := s.Estimate(ctx, pair); first <- err }()
	<-started
	cancel()
	if err := <-first; err != context.Canceled {
		t.Fatalf("closed view error=%v", err)
	}
	unblock()
	if _, err := s.Estimate(context.Background(), pair); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("shared quote refetched after closing a view: %d requests", requests.Load())
	}
}

func TestCacheHasBoundedSize(t *testing.T) {
	entries := make(map[string]*flight[book])
	for i := range 256 {
		entries[fmt.Sprint(i)] = &flight[book]{finished: time.Now()}
	}
	if !makeRoom(entries, 256) || len(entries) != 255 {
		t.Fatal("cache did not evict completed entry")
	}
	entries = map[string]*flight[book]{"busy": {done: make(chan struct{})}}
	if makeRoom(entries, 1) {
		t.Fatal("evicted shared active flight")
	}
}

func TestExecutionQuoteBypassesDisplayCacheAndRefreshesCompletedBooks(t *testing.T) {
	var requests atomic.Int32
	var empty atomic.Bool
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if empty.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		serveTestBook(w, r)
	})
	pair := Pair{Market{"aster", "BTCUSDT"}, Market{"hyperliquid", "BTC"}}
	if _, err := s.Estimate(context.Background(), pair); err != nil {
		t.Fatal(err)
	}
	quote, err := s.Quote(context.Background(), pair, OrderSize{BaseAmount: 6}, false)
	if err != nil || quote.Sufficient || requests.Load() != 2 {
		t.Fatalf("cached books: %+v, %v, requests=%d", quote, err, requests.Load())
	}
	quote, err = s.Quote(context.Background(), pair, OrderSize{Notional: 500}, false)
	if err != nil || !quote.Sufficient || quote.BaseAmount != 5 {
		t.Fatalf("notional sizing did not use current book prices: %+v, %v", quote, err)
	}
	empty.Store(true)
	if _, err := s.Quote(context.Background(), pair, OrderSize{BaseAmount: 1}, true); err == nil {
		t.Fatal("fresh execution reused the successful display cache")
	}
	if requests.Load() != 4 {
		t.Fatalf("fresh reads=%d, want 4", requests.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Quote(ctx, pair, OrderSize{BaseAmount: 1}, true); err != context.Canceled {
		t.Fatal(err)
	}
}

// Explicit opt-in: public market reads only, never orders or credentials.
func TestPublicBooks(t *testing.T) {
	if os.Getenv("ORBITAL_L2_SMOKE") != "1" {
		t.Skip("set ORBITAL_L2_SMOKE=1 for public REST smoke check")
	}
	s := New(context.Background(), slog.Default())
	for _, short := range []Market{{"hyperliquid", "BTC"}, {"pacifica", "BTC"}} {
		result, err := s.Estimate(context.Background(), Pair{Market{"aster", "BTCUSDT"}, short})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("BTC Aster/%s: $%.2f, book limited=%t", short.Venue, result.Notional, result.BookLimited)
	}
}
