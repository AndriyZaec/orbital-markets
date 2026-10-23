package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestPositionPricePnLContextExactlyMatchesMonitorSnapshot(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		opened := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		fills := []executor.LiveFill{
			{Leg: 1, Venue: "aster", Side: "long", Filled: true, FilledAmount: 2.5, AvgFillPrice: 100, FilledAt: opened.Format(time.RFC3339)},
			{Leg: 2, Venue: "pacifica", Side: "short", Filled: true, FilledAmount: 2.4, AvgFillPrice: 101, FilledAt: opened.Format(time.RFC3339)},
		}
		if reverse {
			fills[0], fills[1] = fills[1], fills[0]
			fills[0].Leg, fills[1].Leg = 1, 2
		}
		leg1, leg2 := executor.MonitoredPriceLegs(fills)
		mark1, mark2 := 110.0, 112.0
		if reverse {
			mark1, mark2 = mark2, mark1
		}
		parts := executor.PricePnLAtMarks(*leg1, *leg2, mark1, mark2)
		pos := &executor.LivePosition{State: "open", VenueA: "aster", VenueB: "pacifica", OpenedAt: opened.Format(time.RFC3339),
			MonitorAt: opened.Add(time.Minute).Format(time.RFC3339), Leg1CurPrice: mark1, Leg2CurPrice: mark2, PricePnL: parts.Total}
		ctx := buildPositionPricePnLContext(pos, fills)
		if !ctx.Available || *ctx.Latest.PricePnL != pos.PricePnL || *ctx.Latest.LongPnL != 25 || math.Abs(*ctx.Latest.ShortPnL+26.4) > 1e-12 {
			t.Fatalf("reverse=%t chart/panel mismatch: %+v", reverse, ctx)
		}
		oldKey := ctx.ValuationKey
		fills[0].FilledAmount *= 2
		if got := buildPositionPricePnLContext(pos, fills); got.Available {
			t.Fatal("changed fills must wait for monitoring rather than fabricate matching PnL")
		}
		model, _ := newPositionPricePnLModel(pos, fills)
		if model.key() == oldKey {
			t.Fatal("changed valuation inputs reused the history version")
		}
	}
}

func TestPositionPricePnLHistoryReusesMarketCacheAndEnforcesOwnership(t *testing.T) {
	s, database := newResidualExposureServer(t)
	opened := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`UPDATE live_positions SET state='open', opened_at=? WHERE id='position-residual'`, opened.Format(time.RFC3339))
	mustExec(`UPDATE live_fills SET filled_amount=2.5, avg_fill_price=100, filled_at=? WHERE position_id='position-residual'`, opened.Format(time.RFC3339))
	mustExec(`INSERT INTO live_fills (position_id,leg,venue,symbol,side,requested_amount,filled_amount,avg_fill_price,fill_ratio,fee,accepted,filled,filled_at)
		VALUES ('position-residual',2,'hyperliquid','SOL','short',2.4,2.4,101,1,0,1,1,?)`, opened.Add(10*time.Second).Format(time.RFC3339))
	s.liveStore.UpdateMonitoring(context.Background(), "position-residual", executor.MonitorUpdate{
		Leg1CurPrice: 110, Leg2CurPrice: 112, PricePnL: executor.PricePnLAtMarks(
			executor.LiveFill{Side: "long", AvgFillPrice: 100, FilledAmount: 2.5},
			executor.LiveFill{Side: "short", AvgFillPrice: 101, FilledAmount: 2.4}, 110, 112).Total,
	})
	a, b := 110.0, 112.0
	market := historyResponse{Asset: "SOL", VenueA: "pacifica", VenueB: "hyperliquid", Points: []historyPoint{
		{T: opened.Add(5 * time.Second).Format(time.RFC3339), MarkPriceA: &a, MarkPriceB: &b}, // before leg 2
		{T: opened.Add(time.Minute).Format(time.RFC3339), MarkPriceA: &a, MarkPriceB: &b, Segment: 7},
		{T: opened.Add(2 * time.Minute).Format(time.RFC3339), Segment: 8}, // explicit missing quote
	}}
	s.historyCache = newHistoryCache(8)
	key := historyCacheKey{asset: "SOL", venueA: "pacifica", venueB: "hyperliquid", range_: "24h", priceSpread: true}
	_, _, err := s.historyCache.get(context.Background(), context.Background(), key, time.Hour,
		func(context.Context) (historyResponse, error) { return market, nil })
	if err != nil {
		t.Fatal(err)
	}
	// analyticsDB is intentionally nil. A second market load would fail this test.
	path := "/api/v1/live/positions/position-residual/price-pnl?account_pacifica=sol-wallet&account_hyperliquid=0xwallet"
	w := httptest.NewRecorder()
	s.handlePositionPricePnLHistory(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Points []positionPricePnLPoint `json:"points"`
		Key    string                  `json:"valuation_key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Points) != 2 || response.Key == "" || response.Points[1].PricePnL != nil {
		t.Fatalf("entry filtering, gaps or version are wrong: %+v", response)
	}
	if response.Points[0].Segment == nil || *response.Points[0].Segment != 7 || response.Points[1].Segment == nil || *response.Points[1].Segment != 8 {
		t.Fatal("Price PnL projection lost the shared market history gap boundaries")
	}
	pos, _ := s.liveStore.GetPosition(context.Background(), "position-residual")
	if *response.Points[0].PricePnL != pos.PricePnL || *response.Points[0].LongPnL+*response.Points[0].ShortPnL != pos.PricePnL {
		t.Fatalf("history differs from persisted panel PnL: %+v", response.Points[0])
	}
	if *market.Points[1].MarkPriceA != 110 {
		t.Fatal("position calculation mutated shared market data")
	}
	detailResponse := httptest.NewRecorder()
	s.handleLivePosition(detailResponse, httptest.NewRequest(http.MethodGet,
		"/api/v1/live/positions/position-residual?account_pacifica=sol-wallet&account_hyperliquid=0xwallet", nil))
	var detail struct {
		Position executor.LivePosition `json:"position"`
		Chart    positionChartContext  `json:"chart_context"`
	}
	if detailResponse.Code != http.StatusOK || json.Unmarshal(detailResponse.Body.Bytes(), &detail) != nil ||
		detail.Chart.PricePnL == nil || !detail.Chart.PricePnL.Available || *detail.Chart.PricePnL.Latest.PricePnL != detail.Position.PricePnL {
		t.Fatalf("position detail does not publish one coherent panel/chart snapshot: %s", detailResponse.Body.String())
	}
	w = httptest.NewRecorder()
	s.handlePositionPricePnLHistory(w, httptest.NewRequest(http.MethodGet, "/api/v1/live/positions/position-residual/price-pnl?account_pacifica=other-wallet&account_hyperliquid=0xwallet", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign position leaked: %d", w.Code)
	}
}
