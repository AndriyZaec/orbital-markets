package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type selectedQuoteAdapter struct {
	name string
	data venue.MarketData
}

func (a selectedQuoteAdapter) Name() string { return a.name }
func (a selectedQuoteAdapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	return []venue.MarketData{a.data}, nil
}

type selectedQuoteSource struct {
	pair  liquidity.Pair
	calls int
	fail  bool
}

func (s *selectedQuoteSource) Estimate(_ context.Context, pair liquidity.Pair) (liquidity.Estimate, error) {
	s.pair, s.calls = pair, s.calls+1
	if s.fail {
		return liquidity.Estimate{}, liquidity.ErrUnavailable
	}
	return liquidity.Estimate{Notional: 10000, BookLimited: true, ObservedAt: time.Now()}, nil
}

func TestSelectedOpportunityQuotesDoNotChangeExecutionSizing(t *testing.T) {
	now := time.Now()
	adapter := func(name, key string, funding float64) selectedQuoteAdapter {
		return selectedQuoteAdapter{name, venue.MarketData{Venue: name, Asset: "BTC", MarketKey: key, MarkPrice: 100, IndexPrice: 100, BidPrice: 99.99, AskPrice: 100.01, BidSize: 1000, AskSize: 1000, OpenInterest: 100000, MaxLeverage: 10, FundingRate: funding, Timestamp: now}}
	}
	sc := scanner.New(slog.Default(), adapter("aster", "BTCUSDT", 0.001), adapter("hyperliquid", "BTC", 0.002))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.Run(ctx, time.Hour)
	deadline := time.Now().Add(time.Second)
	for len(sc.Opportunities()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sc.Opportunities()) == 0 {
		t.Fatal("no initial opportunity")
	}
	before := sc.Opportunities()[0]
	source := &selectedQuoteSource{}
	s := &Server{ctx: ctx, logger: slog.Default(), scanner: sc, opportunityLiquidity: source}
	table := httptest.NewRecorder()
	s.handleOpportunities(table, httptest.NewRequest(http.MethodGet, "/api/v1/opportunities", nil))
	if table.Code != http.StatusOK || source.calls != 0 {
		t.Fatal("table request triggered L2 collection")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/opportunities/"+before.ID+"/liquidity", nil)
	request.SetPathValue("opportunityID", before.ID)
	response := httptest.NewRecorder()
	s.handleOpportunityLiquidity(response, request)
	var result struct {
		Available bool    `json:"available"`
		Capacity  float64 `json:"capacity"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Available || result.Capacity != 10000 || source.pair.Long.Key != "BTCUSDT" || source.pair.Short.Key != "BTC" {
		t.Fatalf("quote=%+v, pair=%+v", result, source.pair)
	}
	plan, err := sc.BuildPlan(context.Background(), before.ID, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if plan.BestPriceCapacity != before.BestPriceCapacity || plan.Notional != 100 || sc.Opportunities()[0].RecommendedNotional != before.RecommendedNotional {
		t.Fatal("L2 quote changed execution sizing")
	}
	for _, id := range []string{"unknown", before.ID} {
		source.fail = true
		request.SetPathValue("opportunityID", id)
		response = httptest.NewRecorder()
		s.handleOpportunityLiquidity(response, request)
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || result.Available {
			t.Fatal("optional quote failure blocked view")
		}
	}
	if source.calls != 2 {
		t.Fatal("unknown opportunity triggered venue read")
	}
	if sc.Opportunities()[0].Status != domain.OpportunityAvailable {
		t.Fatal("optional read degraded scanner")
	}
}
