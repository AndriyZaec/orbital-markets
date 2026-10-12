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
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

type executionDepthStub struct {
	quote  liquidity.OrderQuote
	err    error
	amount float64
	fresh  bool
	calls  int
}

func (s *executionDepthStub) Quote(_ context.Context, _ liquidity.Pair, size liquidity.OrderSize, fresh bool) (liquidity.OrderQuote, error) {
	s.calls++
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
	if source.amount != plan.BaseAmount || math.Abs(plan.BaseAmount-500.0/100.1) > 1e-12 {
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

type previewPrecision struct{}

func (previewPrecision) AssetIndex(string) (int, bool)   { return 1, true }
func (previewPrecision) SizeDecimals(string) (int, bool) { return 2, true }

type previewLots map[string]string

func (p previewLots) LotSize(symbol string) (string, bool) { lot, ok := p[symbol]; return lot, ok }

func bboFirstScanner(t *testing.T) (*Scanner, *leverageTestAdapter, *leverageTestAdapter, *executionDepthStub) {
	t.Helper()
	now := time.Now()
	adapter := func(name string, funding float64) *leverageTestAdapter {
		return &leverageTestAdapter{name: name, data: []venue.MarketData{{Venue: name, Asset: "BTC", MarketKey: "BTC",
			MarkPrice: 100, IndexPrice: 100, BidPrice: 100, AskPrice: 100, BidSize: 100, AskSize: 100,
			OpenInterest: 1000, MaxLeverage: 10, FundingRate: funding, Timestamp: now,
			Health: &venue.MarketHealth{Index: venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now},
				Book: venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now}},
		}}}
	}
	a, b := adapter("pacifica", 0.000001), adapter("hyperliquid", 0.000006)
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), a, b)
	s.scan(context.Background())
	if len(s.Opportunities()) == 0 {
		t.Fatal("no opportunity")
	}
	source := &executionDepthStub{quote: liquidity.OrderQuote{Sufficient: true, LongPrice: 100, ShortPrice: 100,
		LongWorstPrice: 100.2, ShortWorstPrice: 99.8, LongImpact: 0.001, ShortImpact: 0.001, ObservedAt: now}}
	s.EnableExecutionLiquidity(source)
	return s, a, b, source
}

func TestBBOCoveredPlansAndExecutionNeverReadL2(t *testing.T) {
	s, a, b, source := bboFirstScanner(t)
	ctx := context.Background()
	for range 1000 {
		plan, err := s.BuildPlan(ctx, s.Opportunities()[0].ID, 2, 90)
		if err != nil || !plan.Executable || plan.Liquidity.Status != "bbo" {
			t.Fatalf("BBO plan: %+v, %v", plan, err)
		}
		if err := s.ValidateExecutionLiquidity(ctx, plan, plan.BaseAmount); err != nil {
			t.Fatal(err)
		}
	}
	if source.calls != 0 {
		t.Fatalf("BBO views consumed %d L2 reads", source.calls)
	}
	plan, err := s.BuildPlan(ctx, s.Opportunities()[0].ID, 2, 100)
	if err != nil || !plan.Executable || source.calls != 0 {
		t.Fatal("full BBO amount triggered L2")
	}
	// If BBO shrinks after approval, final validation must use fresh L2.
	a.data[0].AskSize, b.data[0].BidSize = 50, 50
	if err := s.ValidateExecutionLiquidity(ctx, plan, plan.BaseAmount); err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || !source.fresh {
		t.Fatal("shrinking BBO did not trigger a fresh L2 check")
	}
}

func TestBBOFreshnessUsesBookComponentAndFailsClosed(t *testing.T) {
	s, a, _, source := bboFirstScanner(t)
	for _, health := range []venue.ComponentHealth{
		{Status: venue.HealthAvailable, SourceTime: time.Now().Add(-45 * time.Second)},
		{Status: venue.HealthUnavailable, ReceivedAt: time.Now()},
		{Status: venue.HealthAvailable},
	} {
		a.data[0].Health.Book = health
		plan, err := s.BuildPlan(context.Background(), s.Opportunities()[0].ID, 2, 90)
		if err != nil || plan.Executable || plan.Liquidity.Status != "unavailable" {
			t.Fatalf("stale BBO admitted: %+v, %v", plan, err)
		}
		if err := s.ValidateExecutionLiquidity(context.Background(), plan, plan.BaseAmount); err == nil {
			t.Fatal("stale BBO admitted before submission")
		}
	}
	if source.calls != 0 {
		t.Fatal("ordinary BBO-sized previews started L2 polling")
	}
	a.data[0].Health.Book = venue.ComponentHealth{Status: venue.HealthAvailable, ReceivedAt: time.Now()}
	plan, err := s.BuildPlan(context.Background(), s.Opportunities()[0].ID, 2, 90)
	if err != nil || !plan.Executable {
		t.Fatalf("received-time book fallback failed: %v", err)
	}
}

func TestPreviewNormalizesBeforeDecidingDepthCoverage(t *testing.T) {
	s, _, _, source := bboFirstScanner(t)
	modules, err := venue.NewLiveModuleRegistry(paclive.NewLiveModule(previewLots{"BTC": "0.01"}), hllive.NewLiveModule(previewPrecision{}))
	if err != nil {
		t.Fatal(err)
	}
	s.EnableAmountNormalization(modules.NormalizeHedgeAmount)
	plan, err := s.BuildPlan(context.Background(), s.Opportunities()[0].ID, 2, 100.9)
	if err != nil || !plan.Executable || plan.BaseAmount != 1 || plan.Liquidity.Status != "bbo" {
		t.Fatalf("normalized covered amount was rejected: %+v, %v", plan, err)
	}
	if source.calls != 0 {
		t.Fatal("raw 1.009 amount incorrectly required L2")
	}
	if err := s.ValidateExecutionLiquidity(context.Background(), plan, plan.BaseAmount); err != nil {
		t.Fatal(err)
	}
	plan, err = s.BuildPlan(context.Background(), s.Opportunities()[0].ID, 2, 201.9)
	if err != nil || !plan.Executable || plan.BaseAmount != 2.01 || source.amount != 2.01 || source.calls != 1 {
		t.Fatalf("L2 did not evaluate the normalized amount: %+v, %v", plan, err)
	}
}
