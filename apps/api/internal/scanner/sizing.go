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
	MaxAvailableNotional float64 // observed market capacity (min OI, display only)
	RecommendedNotional  float64 // largest size with acceptable execution quality
	CautionNotionalCap   float64 // hard cap from depth + OI
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

// venueImpact estimates execution cost for one leg using sqrt impact model.
//
// At size=0: cost = half-spread (crossing the spread)
// As size grows: cost increases as sqrt(1 + size/depth)
func venueImpact(size float64, vd venueDepth) float64 {
	if vd.topOfBook <= 0 {
		return vd.spread // no depth info, just return full spread
	}
	return (vd.spread / 2) * math.Sqrt(1+size/vd.topOfBook)
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

	// Walk up from small to large and find the inflection point
	// where total execution cost exceeds what the edge can recoup.
	edgePerHour := domain.DeannualizeRate(annualizedGrossEdge)
	edgeBudget := edgePerHour * assumedHoldHours

	var recommended float64

	for step := 1; step <= sizeSteps; step++ {
		size := cautionCap * float64(step) / float64(sizeSteps)

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
