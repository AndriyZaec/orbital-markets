package domain

// LiveAdmissionResult is the outcome of the live admission gate.
type LiveAdmissionResult struct {
	Allowed bool     `json:"allowed"`
	Reasons []string `json:"reasons,omitempty"`
}

// CheckLiveAdmission evaluates whether an opportunity + plan is allowed into
// the first constrained live execution scope.
//
// Rules:
//  1. Venue pair must be Pacifica + Hyperliquid
//  2. ExecutionStatus must be "executable"
//  3. RiskTier must not be "experimental"
//  4. Liquidity must not be "toxic"
//  5. RecommendedNotional must be > 0
//  6. Leverage must be within allowed range
func CheckLiveAdmission(opp Opportunity, leverage float64) LiveAdmissionResult {
	var reasons []string

	// 1. Venue pair
	validPair := isAllowedVenuePair(opp.VenuePair)
	if !validPair {
		reasons = append(reasons, "venue pair not in live scope (requires pacifica + hyperliquid)")
	}

	// 2. Execution status
	if opp.ExecutionStatus != "executable" {
		reasons = append(reasons, "opportunity is blocked: "+opp.ExecutionStatus)
	}

	// 3. Risk tier
	if opp.RiskTier == RiskExperimental {
		reasons = append(reasons, "experimental risk tier not allowed in live scope")
	}

	// 4. Liquidity
	if opp.Liquidity == LiquidityToxic {
		reasons = append(reasons, "toxic liquidity not allowed in live scope")
	}

	// 5. Recommended notional
	if opp.RecommendedNotional <= 0 {
		reasons = append(reasons, "no viable recommended notional")
	}

	// 6. Leverage
	if !ValidateLeverage(leverage) {
		reasons = append(reasons, "leverage outside allowed range")
	}

	return LiveAdmissionResult{
		Allowed: len(reasons) == 0,
		Reasons: reasons,
	}
}

func isAllowedVenuePair(vp VenuePair) bool {
	a, b := vp.VenueA, vp.VenueB
	return (a == "pacifica" && b == "hyperliquid") ||
		(a == "hyperliquid" && b == "pacifica")
}
