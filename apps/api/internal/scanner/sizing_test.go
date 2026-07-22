package scanner

import (
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestEstimateExecutionSlippageIncreasesWithSize(t *testing.T) {
	market := venue.MarketData{
		BidPrice: 99.9,
		AskPrice: 100.1,
		BidSize:  1000,
		AskSize:  1000,
	}

	small := estimateExecutionSlippage(market, domain.SideLong, 10)
	large := estimateExecutionSlippage(market, domain.SideLong, 5000)
	if large <= small {
		t.Fatalf("large slippage = %v, want greater than small slippage %v", large, small)
	}
}

func TestEstimateExecutionSlippageUsesExecutableSideDepth(t *testing.T) {
	market := venue.MarketData{
		BidPrice: 99.9,
		AskPrice: 100.1,
		BidSize:  10000,
		AskSize:  100,
	}

	longSlippage := estimateExecutionSlippage(market, domain.SideLong, 1000)
	shortSlippage := estimateExecutionSlippage(market, domain.SideShort, 1000)
	if longSlippage <= shortSlippage {
		t.Fatalf("long slippage = %v, want greater than short slippage %v", longSlippage, shortSlippage)
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
