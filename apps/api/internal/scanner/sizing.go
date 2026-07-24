package scanner

import (
	"math"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	// Leave room for quote movement and competing takers while keeping the
	// suggested order inside the weaker visible best-price level.
	suggestedBBOShare = 0.25

	// Applied when bid == ask and the venue only supplied a mid-like quote.
	minHiddenSpread = 0.0005 // 5bps

	thinBestPriceCapacity = 1_000.0
	deepBestPriceCapacity = 10_000.0
)

type SizingResult struct {
	MaxAvailableNotional float64              // min OI, display context only
	BestPriceCapacity    float64              // weaker executable-side BBO notional
	RecommendedNotional  float64              // conservative share of BBO capacity
	Liquidity            domain.LiquidityTier // observable BBO quality
}

// Within the visible best level every unit fills at the same quoted price, so
// crossing half the spread is the only observable slippage. Sizes beyond that
// level are blocked until L2 can price them instead of inventing impact.
func estimateExecutionSlippage(md venue.MarketData, side domain.Side, notional float64) float64 {
	_ = side
	_ = notional
	if md.BidPrice <= 0 || md.AskPrice <= 0 {
		return 0
	}

	spread := relSpread(md)
	if spread <= 0 {
		spread = minHiddenSpread
	}
	return spread / 2
}

func executionSideDepth(md venue.MarketData, side domain.Side) float64 {
	if side == domain.SideLong {
		return md.AskSize
	}
	return md.BidSize
}

func computeSizing(a, b venue.MarketData, direction domain.Direction) SizingResult {
	minOI := math.Min(a.OpenInterest, b.OpenInterest)
	longMarket, shortMarket := marketsForDirection(a, b, direction)
	bestPriceCapacity := math.Min(
		executionSideDepth(longMarket, domain.SideLong),
		executionSideDepth(shortMarket, domain.SideShort),
	)
	if bestPriceCapacity <= 0 {
		return SizingResult{
			MaxAvailableNotional: minOI,
			Liquidity:            domain.LiquidityToxic,
		}
	}

	return SizingResult{
		MaxAvailableNotional: minOI,
		BestPriceCapacity:    bestPriceCapacity,
		RecommendedNotional:  bestPriceCapacity * suggestedBBOShare,
		Liquidity:            classifyLiquidity(bestPriceCapacity),
	}
}

func marketsForDirection(a, b venue.MarketData, direction domain.Direction) (longMarket, shortMarket venue.MarketData) {
	if direction == domain.DirectionLongA {
		return a, b
	}
	return b, a
}

func classifyLiquidity(bestPriceCapacity float64) domain.LiquidityTier {
	switch {
	case bestPriceCapacity <= 0:
		return domain.LiquidityToxic
	case bestPriceCapacity < thinBestPriceCapacity:
		return domain.LiquidityThin
	case bestPriceCapacity < deepBestPriceCapacity:
		return domain.LiquidityMedium
	default:
		return domain.LiquidityDeep
	}
}

func relSpread(md venue.MarketData) float64 {
	if md.BidPrice <= 0 || md.AskPrice <= 0 {
		return 0
	}
	mid := (md.AskPrice + md.BidPrice) / 2
	if mid <= 0 {
		return 0
	}
	return (md.AskPrice - md.BidPrice) / mid
}
