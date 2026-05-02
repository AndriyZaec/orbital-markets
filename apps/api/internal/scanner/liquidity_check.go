package scanner

import (
	"fmt"
	"math"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

// LiquidityCheck runs fake-liquidity detection on a venue pair.
// Returns suspect=true if any signal fires, with reasons explaining why.
// blocking=true means the opportunity should not be executable.
type LiquidityCheck struct {
	Suspect  bool     // at least one signal fired
	Blocking bool     // severe enough to block execution
	Reasons  []string // human-readable signal descriptions
}

// Thresholds — tunable with paper analytics.
const (
	// BBO size < this fraction of OI is suspicious
	minDepthToOIRatio = 0.0001 // 0.01% of OI

	// BBO size below this absolute notional is suspect
	minAbsoluteDepth = 10.0 // $10

	// Bid/ask size ratio beyond this is asymmetric (one-sided book)
	maxDepthAsymmetry = 20.0 // 20:1 ratio

	// Quote older than this is stale
	staleQuoteAge = 60 * time.Second

	// Edge above this with depth below minAbsoluteDepth is too-good-to-be-true
	tooGoodEdgeThreshold = 0.50 // 50% annualized
)

// CheckLiquidity runs all fake-liquidity signals on two venue snapshots.
func CheckLiquidity(a, b venue.MarketData, annualizedEdge float64, now time.Time) LiquidityCheck {
	var reasons []string
	blocking := false

	// Run checks per venue
	for _, md := range []venue.MarketData{a, b} {
		topOfBook := math.Min(md.BidSize, md.AskSize)

		// Signal 1: Missing BBO depth entirely
		if md.BidSize <= 0 && md.AskSize <= 0 {
			reasons = append(reasons, fmt.Sprintf("%s: no BBO depth", md.Venue))
			blocking = true
			continue
		}

		// Signal 2: Tiny absolute depth
		if topOfBook > 0 && topOfBook < minAbsoluteDepth {
			reasons = append(reasons, fmt.Sprintf("%s: top-of-book $%.0f (below $%.0f minimum)", md.Venue, topOfBook, minAbsoluteDepth))
			blocking = true
		}

		// Signal 3: Depth suspiciously small relative to OI
		if md.OpenInterest > 0 && topOfBook > 0 {
			ratio := topOfBook / md.OpenInterest
			if ratio < minDepthToOIRatio {
				reasons = append(reasons, fmt.Sprintf("%s: depth/OI ratio %.4f%% (below %.4f%%)", md.Venue, ratio*100, minDepthToOIRatio*100))
			}
		}

		// Signal 4: Heavily asymmetric book (one-sided)
		if md.BidSize > 0 && md.AskSize > 0 {
			asymmetry := math.Max(md.BidSize, md.AskSize) / math.Min(md.BidSize, md.AskSize)
			if asymmetry > maxDepthAsymmetry {
				reasons = append(reasons, fmt.Sprintf("%s: asymmetric book %.0f:1", md.Venue, asymmetry))
			}
		}

		// Signal 5: Stale quote
		if !md.Timestamp.IsZero() && now.Sub(md.Timestamp) > staleQuoteAge {
			reasons = append(reasons, fmt.Sprintf("%s: quote %.0fs old", md.Venue, now.Sub(md.Timestamp).Seconds()))
		}
	}

	// Signal 6: Too-good-to-be-true — high edge at very low depth
	minTopOfBook := math.Min(
		math.Min(a.BidSize, a.AskSize),
		math.Min(b.BidSize, b.AskSize),
	)
	if annualizedEdge > tooGoodEdgeThreshold && minTopOfBook > 0 && minTopOfBook < minAbsoluteDepth*10 {
		reasons = append(reasons, fmt.Sprintf("edge %.0f%% at $%.0f depth — likely not capturable", annualizedEdge*100, minTopOfBook))
	}

	return LiquidityCheck{
		Suspect:  len(reasons) > 0,
		Blocking: blocking,
		Reasons:  reasons,
	}
}
