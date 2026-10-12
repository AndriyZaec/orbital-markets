package domain

import (
	"fmt"
	"time"
)

type Side string

const (
	SideLong  Side = "long"
	SideShort Side = "short"
)

type Leg struct {
	Venue         string  `json:"venue"`
	Asset         string  `json:"asset"`
	MarketKey     string  `json:"market_key,omitempty"`
	Side          Side    `json:"side"`
	ExpectedPrice float64 `json:"expected_price"`
	Slippage      float64 `json:"slippage"`
	Fee           float64 `json:"fee"`
	DepthImpact   float64 `json:"depth_impact"`
	LimitPrice    float64 `json:"limit_price,omitempty"` // marginal depth price plus existing movement allowance

	// Per-leg leverage and derived margin. Each leg can carry its own leverage;
	// notional stays equal on both legs. MarginRequired = Notional / Leverage.
	Leverage       float64 `json:"leverage"`
	MarginRequired float64 `json:"margin_required"`

	// Estimated liquidation for this leg at this leg's leverage.
	// LiquidationPrice = 0 means not practically liquidatable (1x). See
	// domain/liquidation.go — this is an approximation, not venue-exact math.
	LiquidationPrice    float64      `json:"liquidation_price"`
	LiquidationDistance float64      `json:"liquidation_distance"`
	LiquidationRisk     LiqRiskLevel `json:"liquidation_risk,omitempty"`
}

type Bounds struct {
	MaxSlippagePct    float64 `json:"max_slippage_pct"`
	MaxEntrySpreadPct float64 `json:"max_entry_spread_pct"`
	MinNetEdgePct     float64 `json:"min_net_edge_pct"`
}

type ExecutionPlan struct {
	ID                 string              `json:"id"`
	OpportunityID      string              `json:"opportunity_id"`
	Asset              string              `json:"asset"`
	Direction          Direction           `json:"direction"`
	Notional           float64             `json:"notional"`
	BaseAmount         float64             `json:"base_amount,omitempty"`
	Liquidity          *ExecutionLiquidity `json:"liquidity,omitempty"`
	ImpactAcknowledged bool                `json:"impact_acknowledged,omitempty"`
	BestPriceCapacity  float64             `json:"best_price_capacity"`
	MaxLeverage        int                 `json:"max_leverage"`
	Leverage           LeverageConfig      `json:"leverage"`
	Leg1               Leg                 `json:"leg_1"`
	Leg2               Leg                 `json:"leg_2"`
	ExpectedSpread     float64             `json:"expected_spread"`
	EstimatedNetEdge   float64             `json:"estimated_net_edge"`
	Bounds             Bounds              `json:"bounds"`
	RiskTier           RiskTier            `json:"risk_tier"`
	Confidence         Confidence          `json:"confidence"`
	Executable         bool                `json:"executable"`
	Warnings           []string            `json:"warnings,omitempty"`
	CreatedAt          time.Time           `json:"created_at"`
	ExpiresAt          time.Time           `json:"expires_at"`
}

type ExecutionLiquidity struct {
	Status                  string    `json:"status"` // bbo, l2, insufficient, unavailable
	Impact                  float64   `json:"impact"`
	RequiresAcknowledgement bool      `json:"requires_acknowledgement"`
	ObservedAt              time.Time `json:"observed_at"`
}

func (p *ExecutionPlan) AcknowledgeImpact(acknowledged bool) error {
	if p.Liquidity != nil && p.Liquidity.RequiresAcknowledgement && !acknowledged {
		return fmt.Errorf("acknowledge elevated price impact above 0.2%% before executing")
	}
	p.ImpactAcknowledged = acknowledged
	return nil
}
