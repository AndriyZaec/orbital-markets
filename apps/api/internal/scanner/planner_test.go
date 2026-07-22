package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type leverageTestAdapter struct {
	name string
	data []venue.MarketData
}

func (a leverageTestAdapter) Name() string { return a.name }

func (a leverageTestAdapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	return a.data, nil
}

func TestBuildPlanUsesFreshPairMaximumLeverage(t *testing.T) {
	now := time.Now()
	pac := leverageTestAdapter{name: "pacifica", data: []venue.MarketData{{
		Venue: "pacifica", Asset: "SOL", MarkPrice: 100, IndexPrice: 100,
		FundingRate: 0.0002, BidPrice: 99.9, AskPrice: 100.1,
		BidSize: 1000, AskSize: 1000, OpenInterest: 100000, MaxLeverage: 20, Timestamp: now,
	}}}
	hl := leverageTestAdapter{name: "hyperliquid", data: []venue.MarketData{{
		Venue: "hyperliquid", Asset: "SOL", MarkPrice: 100, IndexPrice: 100,
		FundingRate: 0.0001, BidPrice: 99.9, AskPrice: 100.1,
		BidSize: 1000, AskSize: 1000, OpenInterest: 100000, MaxLeverage: 10, Timestamp: now,
	}}}
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), pac, hl)
	s.scan(context.Background())
	opps := s.Opportunities()
	if len(opps) != 1 {
		t.Fatalf("opportunities = %d, want 1", len(opps))
	}

	plan, err := s.BuildPlan(context.Background(), opps[0].ID, 10, 100)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if plan.MaxLeverage != 10 {
		t.Fatalf("MaxLeverage = %v, want 10", plan.MaxLeverage)
	}
	if plan.Leg1.Leverage != 10 || plan.Leg2.Leverage != 10 {
		t.Fatalf("leg leverage = %v/%v, want 10/10", plan.Leg1.Leverage, plan.Leg2.Leverage)
	}
	largePlan, err := s.BuildPlan(context.Background(), opps[0].ID, 10, 5000)
	if err != nil {
		t.Fatalf("BuildPlan(large notional) error = %v", err)
	}
	if largePlan.Leg1.Slippage <= plan.Leg1.Slippage || largePlan.Leg2.Slippage <= plan.Leg2.Slippage {
		t.Fatalf(
			"large plan slippage = %v/%v, want greater than small plan %v/%v",
			largePlan.Leg1.Slippage, largePlan.Leg2.Slippage,
			plan.Leg1.Slippage, plan.Leg2.Slippage,
		)
	}
	blockedPlan, err := s.BuildPlan(context.Background(), opps[0].ID, 10, 1_000_000_000)
	if err != nil {
		t.Fatalf("BuildPlan(oversized notional) error = %v", err)
	}
	if blockedPlan.Executable {
		t.Fatal("BuildPlan(oversized notional) executable = true, want false")
	}
	if !containsWarning(blockedPlan.Warnings, "exceeds 5% threshold") {
		t.Fatalf("oversized plan warnings = %v, want slippage blocker", blockedPlan.Warnings)
	}

	// Pacifica is the short leg for this funding direction, so bid depth is
	// the executable side that must be present.
	pac.data[0].BidSize = 0
	missingDepthPlan, err := s.BuildPlan(context.Background(), opps[0].ID, 10, 100)
	if err != nil {
		t.Fatalf("BuildPlan(missing depth) error = %v", err)
	}
	if missingDepthPlan.Executable {
		t.Fatal("BuildPlan(missing depth) executable = true, want false")
	}
	if !containsWarning(missingDepthPlan.Warnings, "missing bid depth for short leg") {
		t.Fatalf("missing-depth warnings = %v, want short-leg depth blocker", missingDepthPlan.Warnings)
	}

	_, err = s.BuildPlan(context.Background(), opps[0].ID, 11, 100)
	if err == nil || !strings.Contains(err.Error(), "maximum 10x") {
		t.Fatalf("BuildPlan(11x) error = %v, want pair maximum error", err)
	}
	var leverageErr *LeverageRangeError
	if !errors.As(err, &leverageErr) || leverageErr.PairMax != 10 {
		t.Fatalf("BuildPlan(11x) error = %#v, want LeverageRangeError with pair max 10", err)
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
