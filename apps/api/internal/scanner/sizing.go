package scanner

import (
	"math"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	// sizeSteps is the number of size increments to test.
	sizeSteps = 20

	// maxSizePctOfOI caps recommended size relative to OI as an absolute ceiling.
	maxSizePctOfOI = 0.05

	// maxTopOfBookMultiple caps recommended at N× the weaker venue's top-of-book depth.
	maxTopOfBookMultiple = 10.0

	// assumedHoldHours is the minimum expected hold for edge recoup calculation.
	assumedHoldHours = 4.0

	// minHiddenSpread is applied when bid == ask (mid-only data, no real BBO yet).
	minHiddenSpread = 0.0005 // 5bps
)

// SizingResult contains the separated sizing outputs.
type SizingResult struct {
	MaxAvailableNotional float64              // observed market capacity (min OI, display only)
	RecommendedNotional  float64              // largest size with acceptable execution quality
	CautionNotionalCap   float64              // hard cap from depth + OI
	Liquidity            domain.LiquidityTier // liquidity quality indicator
}

// venueDepth extracts top-of-book depth and spread for one venue.
type venueDepth struct {
	topOfBook float64 // notional at best bid/ask (min of the two)
	spread    float64 // relative spread (ask - bid) / mid
}

func getVenueDepth(md venue.MarketData) venueDepth {
	depth := math.Min(md.BidSize, md.AskSize)

	// Fallback: if BBO sizes not yet available, use conservative OI proxy
	if depth <= 0 && md.OpenInterest > 0 {
		depth = md.OpenInterest * 0.001
	}

	spread := relSpread(md)

	// If bid == ask (mid-only, no real BBO yet), assume a minimum hidden spread
	if spread <= 0 && md.BidPrice > 0 {
		spread = minHiddenSpread
	}

	return venueDepth{topOfBook: depth, spread: spread}
}

// venueImpact estimates the incremental execution cost from size impact.
// Base half-spread (cost of crossing the spread) is excluded — that's a constant
// cost the scanner already accounts for in opportunity ranking.
// This returns only the additional cost caused by order size eating into depth.
//
// Model: incremental_impact = half_spread * (sqrt(1 + size/depth) - 1)
// At size=0: 0 (no incremental cost)
// At size=topOfBook: half_spread * 0.41 (sqrt(2)-1)
// At size=4×topOfBook: half_spread * 1.24 (sqrt(5)-1)
func venueImpact(size float64, vd venueDepth) float64 {
	if vd.topOfBook <= 0 {
		return vd.spread * size * 0.001 // degenerate fallback
	}
	return (vd.spread / 2) * (math.Sqrt(1+size/vd.topOfBook) - 1)
}

// computeSizing determines execution-aware sizing from BBO depth.
//
// Primary input: top-of-book bid/ask sizes from both venues.
// Secondary input: OI as an absolute ceiling only.
//
// The weaker venue naturally constrains sizing — its impact grows faster
// because its topOfBook is smaller.
func computeSizing(a, b venue.MarketData, annualizedGrossEdge float64) SizingResult {
	depthA := getVenueDepth(a)
	depthB := getVenueDepth(b)

	minOI := math.Min(a.OpenInterest, b.OpenInterest)
	minTopOfBook := math.Min(depthA.topOfBook, depthB.topOfBook)

	if minTopOfBook <= 0 || annualizedGrossEdge <= 0 {
		return SizingResult{
			MaxAvailableNotional: minOI,
		}
	}

	// Caution cap: min of depth-based cap and OI-based cap
	depthCap := minTopOfBook * maxTopOfBookMultiple
	oiCap := minOI * maxSizePctOfOI
	cautionCap := math.Min(depthCap, oiCap)

	if cautionCap <= 0 {
		return SizingResult{
			MaxAvailableNotional: minOI,
		}
	}

	// Walk up from small to large using geometric steps.
	// Starts at 0.5% of cautionCap, each step grows by ~1.3×.
	// This explores small rational sizes first before scaling up,
	// preventing the search from skipping the only viable size region.
	edgePerHour := domain.DeannualizeRate(annualizedGrossEdge)
	edgeBudget := edgePerHour * assumedHoldHours

	var recommended float64
	minSize := cautionCap * 0.005 // start at 0.5% of cap
	if minSize < 1 {
		minSize = 1
	}
	// ratio to reach cautionCap in sizeSteps: cautionCap/minSize^(1/steps)
	ratio := math.Pow(cautionCap/minSize, 1.0/float64(sizeSteps))

	for step := 0; step <= sizeSteps; step++ {
		size := minSize * math.Pow(ratio, float64(step))
		if size > cautionCap {
			size = cautionCap
		}

		totalImpact := venueImpact(size, depthA) + venueImpact(size, depthB)

		if totalImpact < edgeBudget {
			recommended = size
		} else {
			break
		}
	}

	return SizingResult{
		MaxAvailableNotional: minOI,
		RecommendedNotional:  recommended,
		CautionNotionalCap:   cautionCap,
		Liquidity:            classifyLiquidity(recommended, cautionCap, minTopOfBook, depthA, depthB),
	}
}

// classifyLiquidity derives a liquidity quality tier from sizing results and depth data.
//
// Logic:
//   - toxic: recommended is 0 or both venues have no real BBO depth
//   - thin: recommended < 10% of caution cap, or weaker venue depth < $100
//   - medium: recommended fills 10-50% of caution cap
//   - deep: recommended fills >50% of caution cap
func classifyLiquidity(recommended, cautionCap, minTopOfBook float64, a, b venueDepth) domain.LiquidityTier {
	if recommended <= 0 {
		return domain.LiquidityToxic
	}

	if a.topOfBook <= 0 || b.topOfBook <= 0 {
		return domain.LiquidityToxic
	}

	if minTopOfBook < 100 {
		return domain.LiquidityThin
	}

	if cautionCap <= 0 {
		return domain.LiquidityThin
	}

	fillRatio := recommended / cautionCap
	switch {
	case fillRatio >= 0.50:
		return domain.LiquidityDeep
	case fillRatio >= 0.10:
		return domain.LiquidityMedium
	default:
		return domain.LiquidityThin
	}
}

// relSpread returns the bid-ask spread relative to mid price.
func relSpread(md venue.MarketData) float64 {
	if md.BidPrice <= 0 || md.AskPrice <= 0 {
		return 0
	}
	mid := (md.BidPrice + md.AskPrice) / 2
	if mid <= 0 {
		return 0
	}
	return (md.AskPrice - md.BidPrice) / mid
}
