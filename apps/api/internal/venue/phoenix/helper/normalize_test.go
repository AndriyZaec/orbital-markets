package helper

import "testing"

func TestNormalizationCannotWidenApprovedBoundaries(t *testing.T) {
	q := NormalizeRequest{Symbol: "SOL", Side: SideBid, Amount: "0.299", LimitPrice: "100.005", BaseLotDecimals: 2, TickSize: "100", MetadataGeneration: 7}
	r := NormalizedOrder{Symbol: q.Symbol, Side: q.Side, Amount: "0.29", LimitPrice: "100", NumBaseLots: 29, PriceLimitTicks: 10000, BaseLotDecimals: 2, TickSize: "100", MetadataGeneration: 7}
	if err := ValidateNormalizedOrder(q, r); err != nil {
		t.Fatal(err)
	}
	bad := r
	bad.Amount = "0.3"
	if err := ValidateNormalizedOrder(q, bad); err == nil {
		t.Fatal("amount widened")
	}
	bad = r
	bad.LimitPrice = "100.01"
	if err := ValidateNormalizedOrder(q, bad); err == nil {
		t.Fatal("buy price widened")
	}
	q.Side = SideAsk
	r.Side = SideAsk
	r.LimitPrice = "100.01"
	r.PriceLimitTicks = 10001
	if err := ValidateNormalizedOrder(q, r); err != nil {
		t.Fatal(err)
	}
	r.LimitPrice = "100"
	if err := ValidateNormalizedOrder(q, r); err == nil {
		t.Fatal("sell price widened")
	}
}
