package domain

// Funding interval assumptions per venue.
// All venue adapters normalize funding_rate to per-hour before storing in MarketData.
//
// Venue intervals:
//   - Pacifica: funding rate per hour (native)
//   - Hyperliquid: funding rate per hour (native)
//
// If a future venue uses a different interval (e.g. 8h like Binance),
// the adapter must convert to per-hour before returning MarketData.

const (
	// HoursPerYear is the canonical annualization constant.
	// All backend edge values are stored as fraction-per-year.
	// UI converts to percent for display.
	HoursPerYear = 8760.0
)

// AnnualizeRate converts a per-hour funding rate to per-year.
func AnnualizeRate(ratePerHour float64) float64 {
	return ratePerHour * HoursPerYear
}

// DeannualizeRate converts a per-year rate back to per-hour.
func DeannualizeRate(ratePerYear float64) float64 {
	return ratePerYear / HoursPerYear
}

// FundingSpread returns the raw funding rate difference (per-hour).
// Positive means A has higher funding than B.
func FundingSpread(rateA, rateB float64) float64 {
	return rateA - rateB
}

// AnnualizedGrossEdge returns the absolute annualized funding spread.
func AnnualizedGrossEdge(rateA, rateB float64) float64 {
	spread := rateA - rateB
	if spread < 0 {
		spread = -spread
	}
	return AnnualizeRate(spread)
}

// CarryEdgePerHour returns the hourly carry for a directed spread position.
// shortRate is collected, longRate is paid.
func CarryEdgePerHour(shortRate, longRate float64) float64 {
	return shortRate - longRate
}

// EstimatedNetEdge returns annualized edge minus one-time entry costs.
// entryCosts is a one-time fractional cost (e.g. 0.001 for 10bps).
func EstimatedNetEdge(grossEdgeAnnualized, entryCosts float64) float64 {
	return grossEdgeAnnualized - entryCosts
}
