package executor

import "github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"

type PricePnLBreakdown struct {
	Leg1  float64
	Leg2  float64
	Total float64
}

// MonitoredPriceLegs preserves the monitor's recorded execution-leg selection.
func MonitoredPriceLegs(fills []LiveFill) (leg1, leg2 *LiveFill) {
	for i := range fills {
		if fills[i].Leg == 1 {
			leg1 = &fills[i]
		} else if fills[i].Leg == 2 {
			leg2 = &fills[i]
		}
	}
	return
}

// PricePnLAtMarks is the common mark-price valuation used by monitoring and
// historical charts. Quantities are actual filled base amounts, not notional.
func PricePnLAtMarks(leg1, leg2 LiveFill, mark1, mark2 float64) PricePnLBreakdown {
	pnl1 := legPricePnL(domain.Side(leg1.Side), leg1.AvgFillPrice, mark1, leg1.FilledAmount)
	pnl2 := legPricePnL(domain.Side(leg2.Side), leg2.AvgFillPrice, mark2, leg2.FilledAmount)
	return PricePnLBreakdown{Leg1: pnl1, Leg2: pnl2, Total: pnl1 + pnl2}
}
