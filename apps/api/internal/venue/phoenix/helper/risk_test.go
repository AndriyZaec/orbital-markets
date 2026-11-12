package helper

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func riskFixture(t *testing.T) RiskRequest {
	t.Helper()
	raw, err := os.ReadFile("testdata/risk.json")
	if err != nil {
		t.Fatal(err)
	}
	var q RiskRequest
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatal(err)
	}
	return q
}

func validRiskResult(q RiskRequest) RiskResult {
	expires, _ := RiskExpiry(q)
	return RiskResult{
		OwnerAuthority: q.OwnerAuthority, AccountGeneration: q.AccountGeneration, AccountSlot: q.AccountSlot,
		AccountSequence: q.AccountSequence, MetadataGeneration: q.MetadataGeneration, AsOfUnixMS: q.AsOfUnixMS, ValidUntilUnixMS: expires,
		QuoteDecimals: 6, EquityQuoteLots: "108000000", EffectiveCollateralQuoteLots: "103000000", AvailableMarginQuoteLots: "92000000",
		InitialMarginQuoteLots: "11000000", MaintenanceMarginQuoteLots: "5500000", UnsettledFundingQuoteLots: "-2000000", UnrealizedPnLQuoteLots: "10000000",
		SpotNotionalQuoteLots: "0", DiscountedSpotQuoteLots: "0", RiskTier: "Safe", LiquidationModel: "rise_local_static",
		StaticLiquidationPricesUSD: map[string]*float64{"SOL": nil},
	}
}

func TestRiskResultBindsAccountUnitsAndShortestInputExpiry(t *testing.T) {
	q := riskFixture(t)
	r := validRiskResult(q)
	now := time.UnixMilli(q.AsOfUnixMS)
	if err := ValidateRiskResult(q, r, now); err != nil {
		t.Fatal(err)
	}
	if r.ValidUntilUnixMS != 1029000 {
		t.Fatal("expiry did not use oldest data input")
	}
	for name, mutate := range map[string]func(*RiskResult){
		"owner":               func(r *RiskResult) { r.OwnerAuthority = "wrong" },
		"sequence":            func(r *RiskResult) { r.AccountSequence++ },
		"generation":          func(r *RiskResult) { r.AccountGeneration++ },
		"metadata":            func(r *RiskResult) { r.MetadataGeneration++ },
		"quote units":         func(r *RiskResult) { r.QuoteDecimals = 12 },
		"extended validity":   func(r *RiskResult) { r.ValidUntilUnixMS++ },
		"missing equity":      func(r *RiskResult) { r.EquityQuoteLots = "" },
		"NaN":                 func(r *RiskResult) { r.AvailableMarginQuoteLots = "NaN" },
		"missing liquidation": func(r *RiskResult) { r.StaticLiquidationPricesUSD = nil },
		"unexpected model":    func(r *RiskResult) { r.LiquidationModel = "hawkeye" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := validRiskResult(q)
			mutate(&bad)
			if err := ValidateRiskResult(q, bad, now); err == nil {
				t.Fatal("accepted invalid risk result")
			}
		})
	}
	if err := ValidateRiskResult(q, r, time.UnixMilli(r.ValidUntilUnixMS+1)); err == nil {
		t.Fatal("accepted expired output")
	}
}

func TestRiskInputsRequireFreshAccountPricesAndParams(t *testing.T) {
	for _, mutate := range []func(*RiskRequest){
		func(q *RiskRequest) { q.AccountObservedUnixMS = q.AsOfUnixMS - 30001 },
		func(q *RiskRequest) { q.ParamsObservedUnixMS = q.AsOfUnixMS - 300001 },
		func(q *RiskRequest) { q.Markets[0].PriceObservedUnixMS = q.AsOfUnixMS - 30001 },
		func(q *RiskRequest) { q.Markets[0].PriceObservedUnixMS = q.AsOfUnixMS + 1 },
		func(q *RiskRequest) { q.MetadataGeneration = 0 },
	} {
		q := riskFixture(t)
		mutate(&q)
		if _, err := RiskExpiry(q); err == nil {
			t.Fatal("accepted stale/incomplete inputs")
		}
	}
}

func TestRiskUsesRealPinnedHelper(t *testing.T) {
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set ORBITAL_PHOENIX_HELPER_TEST_PATH to run the pinned Rust helper contract")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	q := riskFixture(t)
	shift := time.Now().UnixMilli() - q.AsOfUnixMS
	q.AsOfUnixMS += shift
	q.AccountObservedUnixMS += shift
	q.ParamsObservedUnixMS += shift
	for i := range q.Markets {
		q.Markets[i].PriceObservedUnixMS += shift
	}
	r, err := c.ComputeRisk(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.EquityQuoteLots != "108000000" || r.UnsettledFundingQuoteLots != "-2000000" || r.AvailableMarginQuoteLots != "92000000" {
		t.Fatalf("wrong SDK risk values: %+v", r)
	}
	q.CollateralQuoteLots = "-9223372036854775808"
	q.Positions[0].VirtualQuoteLots = "-9223372036854775807"
	q.Positions[0].UnsettledFundingQuoteLots = "0"
	if _, err := c.ComputeRisk(ctx, q); err == nil {
		t.Fatal("SDK overflow returned a risk view")
	}
	if _, err := c.Health(ctx); err != nil {
		t.Fatal("SDK overflow stopped the helper", err)
	}
	q.Markets = []RiskMarket{}
	if _, err := c.ComputeRisk(ctx, q); err == nil {
		t.Fatal("missing exposure metadata returned a risk view")
	}
	if _, err := c.Health(ctx); err != nil {
		t.Fatal("invalid request stopped the helper", err)
	}
}
