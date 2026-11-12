package helper

import (
	"context"
	"os"
	"testing"
	"time"
)

func projectionFixture(t *testing.T) RiskRequest {
	q := riskFixture(t)
	fee, isolated := .00035, false
	q.Markets[0].TakerFeeRate, q.Markets[0].IsolatedOnly = &fee, &isolated
	q.Projection = &OrderProjection{IntentID: "open-1", Symbol: "SOL", Side: "bid", NumBaseLots: "1000", PriceLimitTicks: "11000", QuoteObservedUnixMS: q.AsOfUnixMS - 250, TakerFeeMultiplier: .8, FlightFeeBPS: 2}
	return q
}

func validProjection(q RiskRequest) *ProjectionResult {
	p := q.Projection
	return &ProjectionResult{IntentID: p.IntentID, Symbol: p.Symbol, Side: p.Side, NumBaseLots: p.NumBaseLots, PriceLimitTicks: p.PriceLimitTicks, FlightFeeBPS: 2,
		Model: "rise_full_fill_at_limit", FeePolicy: "undiscounted_taker_ceiling", NotionalQuoteLots: "110000000", ExchangeFeeQuoteLots: "38500", FlightFeeQuoteLots: "22000", TotalFeeQuoteLots: "60500",
		ProjectedBaseLots: "2000", ProjectedEquityQuoteLots: "107939500", ProjectedEffectiveCollateralQuoteLots: "102939500", ProjectedInitialMarginQuoteLots: "22000000", ProjectedMaintenanceMarginQuoteLots: "11000000",
		ProjectedAvailableMarginQuoteLots: "80939500", ProjectedRiskTier: "Safe", MarginSufficient: true}
}

func TestProjectionBindsExactOrderAndShorterExecutionQuoteExpiry(t *testing.T) {
	q := projectionFixture(t)
	r := validRiskResult(q)
	r.Projection = validProjection(q)
	if r.ValidUntilUnixMS != 1004750 {
		t.Fatal("projection did not bind quote TTL")
	}
	if err := ValidateRiskResult(q, r, time.UnixMilli(q.AsOfUnixMS)); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ProjectionResult){
		"intent": func(p *ProjectionResult) { p.IntentID = "other" }, "side": func(p *ProjectionResult) { p.Side = "ask" }, "size": func(p *ProjectionResult) { p.NumBaseLots = "1001" }, "price": func(p *ProjectionResult) { p.PriceLimitTicks = "11001" },
		"Flight": func(p *ProjectionResult) { p.FlightFeeBPS = 0 }, "fee sum": func(p *ProjectionResult) { p.TotalFeeQuoteLots = "1" }, "missing quantity": func(p *ProjectionResult) { p.ProjectedBaseLots = "" },
		"unsafe decision": func(p *ProjectionResult) { p.ProjectedRiskTier = "AtRisk" }, "model": func(p *ProjectionResult) { p.Model = "transaction_simulation" }, "silent blocker": func(p *ProjectionResult) { p.MarginSufficient = false },
	} {
		t.Run(name, func(t *testing.T) {
			bad := validRiskResult(q)
			bad.Projection = validProjection(q)
			mutate(bad.Projection)
			if err := ValidateRiskResult(q, bad, time.UnixMilli(q.AsOfUnixMS)); err == nil {
				t.Fatal("accepted projection mismatch")
			}
		})
	}
	r.Projection = nil
	if err := ValidateRiskResult(q, r, time.UnixMilli(q.AsOfUnixMS)); err == nil {
		t.Fatal("missing projection accepted")
	}
	q.Projection.QuoteObservedUnixMS = q.AsOfUnixMS - 5001
	if _, err := RiskExpiry(q); err == nil {
		t.Fatal("stale execution quote accepted")
	}
}

func TestProjectionUsesRealPinnedSDK(t *testing.T) {
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set helper path for actual SDK projection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	q := projectionFixture(t)
	shift := time.Now().UnixMilli() - q.AsOfUnixMS
	q.AsOfUnixMS += shift
	q.AccountObservedUnixMS += shift
	q.ParamsObservedUnixMS += shift
	q.Projection.QuoteObservedUnixMS += shift
	for i := range q.Markets {
		q.Markets[i].PriceObservedUnixMS += shift
	}
	r, err := c.ComputeRisk(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Projection == nil || !r.Projection.MarginSufficient || r.Projection.TotalFeeQuoteLots != "60500" || r.Projection.ProjectedBaseLots != "2000" {
		t.Fatalf("unexpected actual projection: %+v", r.Projection)
	}
}
