package domain

import "math"

// V1 liquidation price model.
//
// This is an approximation, not venue-exact math.
// Real venue liquidation depends on mark price, margin mode,
// maintenance margin rate, and funding accruals — none of which
// we have direct access to in v1.
//
// Formula:
//   Long leg:  liq_price = entry_price * (1 - 1/leverage + maintenance_margin)
//   Short leg: liq_price = entry_price * (1 + 1/leverage - maintenance_margin)
//
// Where:
//   leverage = user-selected leverage (1x-5x)
//   maintenance_margin = 0.005 (0.5%) — conservative buffer
//
// At 1x long: liq = entry * (1 - 1 + 0.005) = entry * 0.005 ≈ 0 (can't be liquidated)
// At 3x long: liq = entry * (1 - 0.333 + 0.005) = entry * 0.672
// At 5x long: liq = entry * (1 - 0.2 + 0.005) = entry * 0.805
//
// Liquidation distance:
//   Long:  dist = (current_price - liq_price) / current_price
//   Short: dist = (liq_price - current_price) / current_price

const MaintenanceMargin = 0.005 // 0.5% conservative buffer

// LiquidationPrice computes the approximate liquidation price for one leg.
func LiquidationPrice(entryPrice float64, side Side, leverage float64) float64 {
	if leverage <= 0 || entryPrice <= 0 {
		return 0
	}

	switch side {
	case SideLong:
		liq := entryPrice * (1 - 1/leverage + MaintenanceMargin)
		return math.Max(0, liq)
	case SideShort:
		return entryPrice * (1 + 1/leverage - MaintenanceMargin)
	default:
		return 0
	}
}

// LiquidationDistance returns how far current price is from liquidation as a fraction.
// Positive = safe, negative = past liquidation.
func LiquidationDistance(currentPrice, liqPrice float64, side Side) float64 {
	if currentPrice <= 0 || liqPrice <= 0 {
		return 0
	}

	switch side {
	case SideLong:
		return (currentPrice - liqPrice) / currentPrice
	case SideShort:
		return (liqPrice - currentPrice) / currentPrice
	default:
		return 0
	}
}
