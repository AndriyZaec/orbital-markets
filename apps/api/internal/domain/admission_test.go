package domain

import "testing"

func TestLiveAdmissionAllowsExperimentalRiskWithWarningHandledByPlan(t *testing.T) {
	opportunity := Opportunity{
		VenuePair:           VenuePair{VenueA: "pacifica", VenueB: "hyperliquid"},
		ExecutionStatus:     "executable",
		RiskTier:            RiskExperimental,
		Liquidity:           LiquidityDeep,
		RecommendedNotional: 100,
	}

	result := CheckLiveAdmission(opportunity, 2, 10)
	if !result.Allowed {
		t.Fatalf("experimental opportunity was blocked: %v", result.Reasons)
	}
}

func TestLiveAdmissionAllowsAsterPairs(t *testing.T) {
	for _, pair := range []VenuePair{
		{VenueA: "aster", VenueB: "pacifica"},
		{VenueA: "hyperliquid", VenueB: "aster"},
	} {
		result := CheckLiveAdmission(Opportunity{
			VenuePair: pair, ExecutionStatus: "executable", Liquidity: LiquidityDeep,
			RecommendedNotional: 100,
		}, 2, 10)
		if !result.Allowed {
			t.Fatalf("Aster pair %+v was blocked: %v", pair, result.Reasons)
		}
	}
}
