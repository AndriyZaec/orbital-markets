package domain

import "time"

type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

type RiskTier string

const (
	RiskConservative RiskTier = "conservative"
	RiskStandard     RiskTier = "standard"
	RiskAggressive   RiskTier = "aggressive"
	RiskExperimental RiskTier = "experimental"
)

type LiquidityTier string

const (
	LiquidityDeep   LiquidityTier = "deep"
	LiquidityMedium LiquidityTier = "medium"
	LiquidityThin   LiquidityTier = "thin"
	LiquidityToxic  LiquidityTier = "toxic"
)

type Direction string

const (
	DirectionLongA Direction = "long_a_short_b"
	DirectionLongB Direction = "long_b_short_a"
)

type VenuePair struct {
	VenueA string `json:"venue_a"`
	VenueB string `json:"venue_b"`
}

type Opportunity struct {
	ID         string    `json:"id"`
	DetectedAt time.Time `json:"detected_at"`
	Asset      string    `json:"asset"`
	VenuePair  VenuePair `json:"venue_pair"`
	Direction  Direction `json:"direction"`

	// Spread metrics
	FundingRateA        float64 `json:"funding_rate_a"`
	FundingRateB        float64 `json:"funding_rate_b"`
	FundingSpread       float64 `json:"funding_spread"`
	AnnualizedGrossEdge float64 `json:"annualized_gross_edge"`
	EntrySpreadEstimate float64 `json:"entry_spread_estimate"`
	SlippageEstimate    float64 `json:"slippage_estimate"`
	FeeEstimate         float64 `json:"fee_estimate"`
	EstimatedNetEdge    float64 `json:"estimated_net_edge"`

	// Sizing
	AvailableNotional   float64 `json:"available_notional"`
	RecommendedNotional float64 `json:"recommended_notional"`

	// Classification
	Liquidity  LiquidityTier `json:"liquidity"`
	Confidence Confidence    `json:"confidence"`
	RiskTier   RiskTier   `json:"risk_tier"`
	Executable bool       `json:"executable"`
	Warnings   []string   `json:"warnings,omitempty"`
}
