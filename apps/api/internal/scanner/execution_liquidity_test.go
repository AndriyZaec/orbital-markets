package scanner

import (
	"context"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type executionDepthStub struct {
	quote  liquidity.OrderQuote
	err    error
	amount float64
	fresh  bool
}

func (s *executionDepthStub) Quote(_ context.Context, _ liquidity.Pair, size liquidity.OrderSize, fresh bool) (liquidity.OrderQuote, error) {
	amount := size.BaseAmount
	if size.Notional > 0 {
		amount = size.Notional / max(s.quote.LongPrice, s.quote.ShortPrice)
	}
	s.amount, s.fresh = amount, fresh
	quote := s.quote
	quote.BaseAmount = amount
	return quote, s.err
}

func TestPlanDepthSupportsAcknowledgedImpactAndRejectsInsufficientDepth(t *testing.T) {
	now := time.Now()
	market := func(name, key string, funding float64) leverageTestAdapter {
		return leverageTestAdapter{name: name, data: []venue.MarketData{{Venue: name, Asset: "BTC", MarketKey: key,
			MarkPrice: 100, IndexPrice: 100, BidPrice: 99.9, AskPrice: 100.1, BidSize: 100, AskSize: 100,
			OpenInterest: 100000, MaxLeverage: 10, FundingRate: funding, Timestamp: now}}}
	}
	long, short := market("aster", "BTCUSDT", 0.001), market("hyperliquid", "BTC", 0.002)
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), long, short)
	s.scan(context.Background())
	opp := s.Opportunities()[0]
	if opp.RecommendedNotional != 90 {
		t.Fatal("suggested size is not 90% BBO")
	}
	source := &executionDepthStub{quote: liquidity.OrderQuote{Sufficient: true, LongPrice: 105, ShortPrice: 95,
		LongImpact: 0.06, ShortImpact: 0.04, LongWorstPrice: 115, ShortWorstPrice: 90, ObservedAt: now}}
	s.EnableExecutionLiquidity(source)
	plan, err := s.BuildPlan(context.Background(), opp.ID, 2, 500)
	if err != nil || !plan.Executable || !plan.Liquidity.RequiresAcknowledgement || math.Abs(plan.Liquidity.Impact-0.1) > 1e-12 {
		t.Fatalf("high impact must be acknowledgeable, not blocked: %+v, %v", plan, err)
	}
	if source.amount != plan.BaseAmount || math.Abs(plan.BaseAmount-500.0/105) > 1e-12 {
		t.Fatal("legs were sized independently")
	}
	if math.Abs(plan.Leg1.LimitPrice-115*1.005) > 1e-12 || math.Abs(plan.Leg2.LimitPrice-90*0.995) > 1e-12 {
		t.Fatal("limits do not reach marginal depth")
	}
	if math.Abs(plan.Leg1.MarginRequired-plan.BaseAmount*105*1.06/2) > 1e-10 {
		t.Fatal("margin ignores expected depth impact")
	}
	before := plan.Leg1.Slippage
	if err := s.EvaluateExecutionLiquidity(context.Background(), plan, liquidity.OrderSize{BaseAmount: plan.BaseAmount}, true); err != nil || plan.Leg1.Slippage != before || !source.fresh {
		t.Fatalf("refresh accumulated slippage or used display data: %v", err)
	}
	source.quote.Sufficient = false
	blocked, err := s.BuildPlan(context.Background(), opp.ID, 2, 500)
	if err != nil || blocked.Executable || blocked.Liquidity.Status != "insufficient" {
		t.Fatalf("insufficient quote: %+v, %v", blocked, err)
	}
	source.err = liquidity.ErrUnavailable
	fallback, err := s.BuildPlan(context.Background(), opp.ID, 2, 100)
	if err != nil || !fallback.Executable || fallback.Liquidity.Status != "bbo" {
		t.Fatalf("BBO fallback: %+v, %v", fallback, err)
	}
	blocked, err = s.BuildPlan(context.Background(), opp.ID, 2, 500)
	if err != nil || blocked.Executable || blocked.Liquidity.Status != "unavailable" {
		t.Fatalf("unknown depth beyond BBO: %+v, %v", blocked, err)
	}
}
