package scanner

import (
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const freshThreshold = 10 * time.Second

func snapshotFresh(md venue.MarketData, now time.Time) bool {
	return !md.Timestamp.IsZero() && now.Sub(md.Timestamp) <= freshThreshold
}

func hasBidAsk(md venue.MarketData) bool {
	return md.BidPrice > 0 && md.AskPrice > 0
}

func hasFunding(md venue.MarketData) bool {
	return md.FundingRate != 0
}

func hasPrice(md venue.MarketData) bool {
	return md.MarkPrice > minMarkPrice && md.IndexPrice > minMarkPrice
}

// classifyConfidence determines confidence level based on data completeness and freshness.
//
// High: both venues fresh, bid/ask present on both, funding present on both, prices not stale.
// Medium: funding + price exists on both, but bid/ask missing or less fresh on one/both.
// Low: partial market data — missing funding, price, or severely stale.
func classifyConfidence(a, b venue.MarketData, now time.Time) domain.Confidence {
	aFresh := snapshotFresh(a, now)
	bFresh := snapshotFresh(b, now)
	aBidAsk := hasBidAsk(a)
	bBidAsk := hasBidAsk(b)
	aFunding := hasFunding(a)
	bFunding := hasFunding(b)
	aPrice := hasPrice(a)
	bPrice := hasPrice(b)

	// High: everything present and fresh on both
	if aFresh && bFresh && aBidAsk && bBidAsk && aFunding && bFunding && aPrice && bPrice {
		return domain.ConfidenceHigh
	}

	// Medium: funding + price on both, but bid/ask incomplete or not fully fresh
	if aFunding && bFunding && aPrice && bPrice {
		return domain.ConfidenceMedium
	}

	// Low: anything else
	return domain.ConfidenceLow
}

// recommendedNotional returns an initial conservative sizing heuristic as a fraction of available liquidity.
// Lower risk tiers get a larger share since the trade is more predictable.
func recommendedNotional(available float64, risk domain.RiskTier) float64 {
	var pct float64
	switch risk {
	case domain.RiskConservative:
		pct = 0.05
	case domain.RiskStandard:
		pct = 0.03
	case domain.RiskAggressive:
		pct = 0.02
	case domain.RiskExperimental:
		pct = 0.01
	default:
		pct = 0.01
	}
	return available * pct
}

func classifyRisk(annualizedGross, entrySpread float64) domain.RiskTier {
	// If entry cost eats more than half the annualized edge, it's aggressive.
	if entrySpread > 0 && annualizedGross > 0 {
		ratio := entrySpread / annualizedGross
		if ratio > 0.5 {
			return domain.RiskExperimental
		}
		if ratio > 0.2 {
			return domain.RiskAggressive
		}
	}

	switch {
	case annualizedGross > 0.50: // >50% annualized
		return domain.RiskExperimental
	case annualizedGross > 0.20: // >20%
		return domain.RiskAggressive
	case annualizedGross > 0.05: // >5%
		return domain.RiskStandard
	default:
		return domain.RiskConservative
	}
}
