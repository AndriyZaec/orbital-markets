package executor

import (
	"math"
	"testing"
)

func TestPricePnLAtMarksUsesFilledQuantitiesInExecutionLegOrder(t *testing.T) {
	long := LiveFill{Leg: 1, Side: "long", FilledAmount: 2.5, AvgFillPrice: 100, Filled: true}
	short := LiveFill{Leg: 2, Side: "short", FilledAmount: 2.4, AvgFillPrice: 101, Filled: true}
	parts := PricePnLAtMarks(long, short, 110, 112)
	if parts.Leg1 != 25 || math.Abs(parts.Leg2+26.4) > 1e-12 || math.Abs(parts.Total+1.4) > 1e-12 {
		t.Fatalf("incorrect unequal-size valuation: %+v", parts)
	}
	parts = PricePnLAtMarks(short, long, 112, 110)
	if math.Abs(parts.Total+1.4) > 1e-12 || parts.Leg2 != 25 {
		t.Fatalf("execution order changed the valuation: %+v", parts)
	}
	fills := []LiveFill{short, long}
	leg1, leg2 := MonitoredPriceLegs(fills)
	if leg1.Leg != 1 || leg2.Leg != 2 {
		t.Fatal("fill slice order changed the monitoring legs")
	}
}
