package scanner

import (
	"math"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

// Sizing constants — tunable later with paper analytics.
const (
	// sizeSteps is the number of size increments to test.
	sizeSteps = 20

	// maxSizePctOfOI caps recommended size as a fraction of min OI
	// regardless of edge quality.
	maxSizePctOfOI = 0.05 // never recommend more than 5% of min OI
)

// SizingResult contains the separated sizing outputs.
type SizingResult struct {
	MaxAvailableNotional float64 // observed market capacity (min OI)
	RecommendedNotional  float64 // largest size with acceptable execution quality
	CautionNotionalCap   float64 // hard cap based on market depth
}

// computeSizing determines execution-aware sizing from market data.
//
// Model (v1):
//   - Entry cost for a given size = base_spread + estimated_slippage(size) + fees
//   - Slippage scales with size relative to market depth (min OI as proxy)
//   - Net edge at size = annualized_funding_edge - entry_cost(size) * annualization_factor
//   - Recommended = largest size where net_edge(size) >= minAcceptableNetEdge
//
// Slippage model:
//   - slippage(size) = base_half_spread * (1 + size / comfortable_depth)
//   - comfortable_depth = 1% of min OI (the size range where impact is minimal)
//
// This is an approximation — no real orderbook depth. Good enough for v1
// and easy to calibrate with paper analytics results.
func computeSizing(a, b venue.MarketData, annualizedGrossEdge float64) SizingResult {
	minOI := math.Min(a.OpenInterest, b.OpenInterest)
	if minOI <= 0 {
		return SizingResult{}
	}

	cautionCap := minOI * maxSizePctOfOI

	// Base spread from current bid/ask on each venue
	baseSpreadA := relSpread(a)
	baseSpreadB := relSpread(b)
	baseTotalSpread := baseSpreadA + baseSpreadB

	// Comfortable depth: the size range where slippage is close to base
	comfortableDepth := minOI * 0.01

	if comfortableDepth <= 0 || annualizedGrossEdge <= 0 {
		return SizingResult{
			MaxAvailableNotional: minOI,
			CautionNotionalCap:   cautionCap,
		}
	}

	// Walk up from small to large and find the inflection point
	// where net edge drops below minimum acceptable.
	//
	// Entry cost is a one-time cost paid at open. To compare it against
	// annualized funding edge, we express it as: what annualized rate
	// does this one-time cost consume if the position is held for an
	// estimated hold period?
	//
	// Entry cost is acceptable if the edge can recoup it within a reasonable
	// hold window. We use: edge_per_hour * assumed_hold_hours > entry_cost.
	const assumedHoldHours = 4.0 // conservative assumed hold for v1
	edgePerHour := domain.DeannualizeRate(annualizedGrossEdge)

	var recommended float64

	for step := 1; step <= sizeSteps; step++ {
		size := cautionCap * float64(step) / float64(sizeSteps)

		entryCost := estimateEntryCost(size, baseTotalSpread, comfortableDepth)
		edgeBudget := edgePerHour * assumedHoldHours

		if entryCost < edgeBudget {
			recommended = size
		} else {
			break
		}
	}

	return SizingResult{
		MaxAvailableNotional: minOI,
		RecommendedNotional:  recommended,
		CautionNotionalCap:   cautionCap,
	}
}

// estimateEntryCost returns the incremental execution cost from sizing.
// This is the additional cost above the base spread+fees that the scanner
// already accounts for in opportunity ranking. Only the size-dependent
// slippage matters here — base fees and base spread are constant regardless
// of size.
func estimateEntryCost(size, baseTotalSpread, comfortableDepth float64) float64 {
	// Additional slippage from size impact
	sizeImpact := (size / comfortableDepth) * baseTotalSpread / 2
	return sizeImpact
}

// relSpread returns the bid-ask spread relative to mid price.
func relSpread(md venue.MarketData) float64 {
	if md.BidPrice <= 0 || md.AskPrice <= 0 {
		return 0
	}
	mid := (md.BidPrice + md.AskPrice) / 2
	return (md.AskPrice - md.BidPrice) / mid
}
