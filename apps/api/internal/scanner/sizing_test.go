package scanner

import (
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestEstimateExecutionSlippageDoesNotInventImpactWithinBestLevel(t *testing.T) {
	market := venue.MarketData{
		BidPrice: 99.9,
		AskPrice: 100.1,
		BidSize:  1000,
		AskSize:  1000,
	}

	small := estimateExecutionSlippage(market, domain.SideLong, 10)
	large := estimateExecutionSlippage(market, domain.SideLong, 1000)
	if large != small {
		t.Fatalf("best-level slippage = %v, want same as small slippage %v", large, small)
	}
}

func TestExecutionSideDepthUsesTheSideThatWillBeConsumed(t *testing.T) {
	market := venue.MarketData{
		BidPrice: 99.9,
		AskPrice: 100.1,
		BidSize:  10000,
		AskSize:  100,
	}

	if depth := executionSideDepth(market, domain.SideLong); depth != 100 {
		t.Fatalf("long depth = %v, want ask depth 100", depth)
	}
	if depth := executionSideDepth(market, domain.SideShort); depth != 10000 {
		t.Fatalf("short depth = %v, want bid depth 10000", depth)
	}
}

func TestExecutionSideDepthDoesNotFallbackToOpenInterest(t *testing.T) {
	market := venue.MarketData{OpenInterest: 1_000_000}
	if depth := executionSideDepth(market, domain.SideLong); depth != 0 {
		t.Fatalf("long execution depth = %v, want 0 without ask depth", depth)
	}
	if depth := executionSideDepth(market, domain.SideShort); depth != 0 {
		t.Fatalf("short execution depth = %v, want 0 without bid depth", depth)
	}
}

func TestComputeSizingUsesEntryExecutableSides(t *testing.T) {
	a := venue.MarketData{
		BidSize: 50, AskSize: 800, OpenInterest: 100_000,
	}
	b := venue.MarketData{
		BidSize: 400, AskSize: 20, OpenInterest: 200_000,
	}

	longA := computeSizing(a, b, domain.DirectionLongA)
	if longA.BestPriceCapacity != 400 {
		t.Fatalf("long-A capacity = %v, want 400 from min(A ask, B bid)", longA.BestPriceCapacity)
	}
	if longA.RecommendedNotional != 100 {
		t.Fatalf("long-A suggested size = %v, want 100", longA.RecommendedNotional)
	}

	longB := computeSizing(a, b, domain.DirectionLongB)
	if longB.BestPriceCapacity != 20 {
		t.Fatalf("long-B capacity = %v, want 20 from min(B ask, A bid)", longB.BestPriceCapacity)
	}
	if longB.RecommendedNotional != 5 {
		t.Fatalf("long-B suggested size = %v, want 5", longB.RecommendedNotional)
	}
}

func TestComputeSizingIsIndependentOfFundingEdge(t *testing.T) {
	a := venue.MarketData{BidSize: 500, AskSize: 500, OpenInterest: 100_000}
	b := venue.MarketData{BidSize: 800, AskSize: 800, OpenInterest: 100_000}

	lowEdge := computeSizing(a, b, domain.DirectionLongA)
	a.FundingRate = 0.01
	b.FundingRate = -0.01
	highEdge := computeSizing(a, b, domain.DirectionLongA)

	if lowEdge.RecommendedNotional != highEdge.RecommendedNotional {
		t.Fatalf("suggested size changed with funding: %v != %v", lowEdge.RecommendedNotional, highEdge.RecommendedNotional)
	}
}
