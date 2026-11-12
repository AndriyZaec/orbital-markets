package domain

import "testing"

func TestLiveAdmissionAllowsAllSupportedVenuePairs(t *testing.T) {
	for _, a := range []string{"pacifica", "hyperliquid", "aster", "phoenix"} {
		for _, b := range []string{"pacifica", "hyperliquid", "aster", "phoenix", "unknown"} {
			result := CheckLiveAdmission(Opportunity{VenuePair: VenuePair{VenueA: a, VenueB: b},
				ExecutionStatus: "executable", Liquidity: LiquidityDeep, RecommendedNotional: 100}, 2, 10)
			want := a != b && b != "unknown"
			if result.Allowed != want {
				t.Errorf("%s/%s allowed=%t, want %t: %v", a, b, result.Allowed, want, result.Reasons)
			}
		}
	}
}

func TestPhoenixAdmissionStillRequiresExecutionLiquidityNotionalAndLeverage(t *testing.T) {
	for _, constraint := range []string{"execution", "liquidity", "notional", "leverage"} {
		t.Run(constraint, func(t *testing.T) {
			opp := Opportunity{VenuePair: VenuePair{VenueA: "phoenix", VenueB: "hyperliquid"},
				ExecutionStatus: "executable", Liquidity: LiquidityDeep, RecommendedNotional: 100}
			leverage := 2.0
			switch constraint {
			case "execution":
				opp.ExecutionStatus = "blocked"
			case "liquidity":
				opp.Liquidity = LiquidityToxic
			case "notional":
				opp.RecommendedNotional = 0
			case "leverage":
				leverage = 11
			}
			if result := CheckLiveAdmission(opp, leverage, 10); result.Allowed || len(result.Reasons) != 1 {
				t.Fatalf("constraint %s bypassed or venue scope still rejected: %+v", constraint, result)
			}
		})
	}
}

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
