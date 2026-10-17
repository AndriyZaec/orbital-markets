package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type visibilityMarketAdapter struct {
	name    string
	mu      sync.RWMutex
	data    []venue.MarketData
	updates chan struct{}
}

func (a *visibilityMarketAdapter) Name() string                       { return a.name }
func (a *visibilityMarketAdapter) MarketDataUpdates() <-chan struct{} { return a.updates }
func (a *visibilityMarketAdapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]venue.MarketData(nil), a.data...), nil
}

func TestAsterOpportunitiesDisappearAndRecoverWithoutHealthUI(t *testing.T) {
	for _, failure := range []string{"missing_snapshot", "missing_oi", "missing_book"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			now := time.Now()
			adapters := make([]venue.Adapter, 0, 3)
			var aster *visibilityMarketAdapter
			for i, name := range []string{"aster", "hyperliquid", "pacifica"} {
				a := &visibilityMarketAdapter{name: name, updates: make(chan struct{}, 1)}
				for _, asset := range []string{"BIO", "SOL"} {
					a.data = append(a.data, venue.MarketData{Venue: name, Asset: asset, MarketKey: asset,
						MarkPrice: 100, IndexPrice: 100, BidPrice: 100, AskPrice: 100, BidSize: 1000, AskSize: 1000,
						OpenInterest: 100000, MaxLeverage: 10, FundingRate: float64(i+1) * 0.001, Timestamp: now})
				}
				if name == "aster" {
					aster = a
				}
				adapters = append(adapters, a)
			}
			sc := scanner.New(logger, adapters...)
			go sc.Run(ctx, time.Hour)
			deadline := time.Now().Add(time.Second)
			for len(sc.Opportunities()) != 6 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if len(sc.Opportunities()) != 6 {
				t.Fatal("expected six healthy asset/venue pairs")
			}
			s := &Server{ctx: ctx, logger: logger, scanner: sc}
			read := func(path string) []domain.Opportunity {
				t.Helper()
				w := httptest.NewRecorder()
				s.handleOpportunities(w, httptest.NewRequest(http.MethodGet, path, nil))
				var rows []domain.Opportunity
				if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK {
					t.Fatalf("opportunities status=%d", w.Code)
				}
				return rows
			}
			initial := read("/api/v1/opportunities")
			if len(initial) != 6 {
				t.Fatalf("initial rows=%d", len(initial))
			}
			aster.mu.Lock()
			original := append([]venue.MarketData(nil), aster.data...)
			switch failure {
			case "missing_snapshot":
				aster.data = aster.data[1:]
			case "missing_oi":
				aster.data[0].OpenInterest = 0
			case "missing_book":
				aster.data[0].BidPrice = 0
			}
			aster.mu.Unlock()
			// Do not notify yet: even a cached available scan must not advertise
			// an Aster market that has already lost its current quote/readiness.
			rows := read("/api/v1/opportunities")
			if len(rows) != 4 {
				t.Fatalf("unready BIO/Aster pairs were not omitted: %+v", rows)
			}
			for _, row := range rows {
				if row.Asset == "BIO" && (row.VenuePair.VenueA == "aster" || row.VenuePair.VenueB == "aster") {
					t.Fatalf("unready Aster row exposed: %+v", row)
				}
			}
			if limited := read("/api/v1/opportunities?limit=4"); len(limited) != 4 {
				t.Fatalf("hidden rows consumed response limit: %d", len(limited))
			}
			// A health-loss notification makes the cached rows degraded. They
			// must remain absent, rather than render a health/error state.
			aster.updates <- struct{}{}
			deadline = time.Now().Add(time.Second)
			changed := false
			for failure != "missing_book" && time.Now().Before(deadline) {
				for _, row := range sc.Opportunities() {
					if row.Asset == "BIO" && (row.VenuePair.VenueA == "aster" || row.VenuePair.VenueB == "aster") && row.Status != domain.OpportunityAvailable {
						changed = true
					}
				}
				if changed {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if failure != "missing_book" && !changed {
				t.Fatal("scanner did not observe the loss notification")
			}
			aster.mu.Lock()
			aster.data = original
			aster.mu.Unlock()
			if failure != "missing_book" {
				if got := read("/api/v1/opportunities"); len(got) != 4 {
					t.Fatalf("degraded cached Aster rows became visible before recovery scan: %+v", got)
				}
			}
			aster.updates <- struct{}{}
			deadline = time.Now().Add(time.Second)
			var recovered []domain.Opportunity
			for time.Now().Before(deadline) {
				recovered = read("/api/v1/opportunities")
				if len(recovered) == 6 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if len(recovered) != 6 {
				t.Fatalf("Aster recovery was not republished: %+v", recovered)
			}
			ids := make(map[string]bool)
			for _, row := range initial {
				ids[row.ID] = true
			}
			for _, row := range recovered {
				if !ids[row.ID] || row.Status != domain.OpportunityAvailable {
					t.Fatalf("recovery changed normal row identity/state: %+v", row)
				}
			}
		})
	}
}
