package domain

import "time"

type Side string

const (
	SideLong  Side = "long"
	SideShort Side = "short"
)

type Leg struct {
	Venue         string  `json:"venue"`
	Asset         string  `json:"asset"`
	Side          Side    `json:"side"`
	ExpectedPrice float64 `json:"expected_price"`
	Slippage      float64 `json:"slippage"`
	Fee           float64 `json:"fee"`
}

type Bounds struct {
	MaxSlippagePct    float64 `json:"max_slippage_pct"`
	MaxEntrySpreadPct float64 `json:"max_entry_spread_pct"`
	MinNetEdgePct     float64 `json:"min_net_edge_pct"`
}

type ExecutionPlan struct {
	ID              string    `json:"id"`
	OpportunityID   string    `json:"opportunity_id"`
	Asset           string    `json:"asset"`
	Direction       Direction `json:"direction"`
	Notional        float64   `json:"notional"`
	Leg1            Leg       `json:"leg_1"`
	Leg2            Leg       `json:"leg_2"`
	ExpectedSpread  float64   `json:"expected_spread"`
	EstimatedNetEdge float64  `json:"estimated_net_edge"`
	Bounds          Bounds    `json:"bounds"`
	Confidence      Confidence `json:"confidence"`
	Executable      bool      `json:"executable"`
	Warnings        []string  `json:"warnings,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}
