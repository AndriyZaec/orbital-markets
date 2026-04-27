package paper

import (
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

// ExecState matches the execution state machine from EXECUTION.md.
type ExecState string

const (
	StatePlanned          ExecState = "planned"
	StateSubmittingLeg1   ExecState = "submitting_leg_1"
	StateAwaitingLeg1Fill ExecState = "awaiting_leg_1_fill"
	StateSubmittingLeg2   ExecState = "submitting_leg_2"
	StateAwaitingLeg2Fill ExecState = "awaiting_leg_2_fill"
	StateRetryingLeg2     ExecState = "retrying_leg_2"
	StateUnwinding        ExecState = "unwinding"
	StateOpen             ExecState = "open"
	StateDegraded         ExecState = "degraded"
	StateFailed           ExecState = "failed"
	StatePendingClose     ExecState = "pending_close"
	StateClosing          ExecState = "closing"
	StateClosed           ExecState = "closed"
)

type CloseReason string

const (
	CloseReasonManual      CloseReason = "manual"
	CloseReasonEdgeCollapse CloseReason = "edge_collapse"
	CloseReasonDegraded    CloseReason = "degraded"
	CloseReasonMaxDuration CloseReason = "max_duration"
)

// Fill represents a simulated fill for one leg.
type Fill struct {
	Venue       string      `json:"venue"`
	Side        domain.Side `json:"side"`
	TargetSize  float64     `json:"target_size"`
	FilledSize  float64     `json:"filled_size"`
	FillPrice   float64     `json:"fill_price"`
	Slippage    float64     `json:"slippage"`
	Fee         float64     `json:"fee"`
	FilledAt    time.Time   `json:"filled_at"`

	// Live leg metrics (updated by monitor)
	CurrentPrice    float64    `json:"current_price"`
	CurrentFunding  float64    `json:"current_funding"`   // current hourly funding rate on this venue
	AccumFunding    float64    `json:"accum_funding"`     // accumulated funding P&L for this leg
	NextFundingAt   *time.Time `json:"next_funding_at"`   // next funding timestamp (best-effort)
	LegPricePnL     float64    `json:"leg_price_pnl"`     // unrealized price P&L for this leg
}

// FillRatio returns filled / target.
func (f Fill) FillRatio() float64 {
	if f.TargetSize == 0 {
		return 0
	}
	return f.FilledSize / f.TargetSize
}

// Event records a state transition for debuggability.
type Event struct {
	FromState ExecState `json:"from_state"`
	ToState   ExecState `json:"to_state"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

// Position is a paper trading position with full lifecycle tracking.
type Position struct {
	ID              string         `json:"id"`
	PlanID          string         `json:"plan_id"`
	OpportunityID   string         `json:"opportunity_id"`
	Asset           string         `json:"asset"`
	Direction       domain.Direction `json:"direction"`
	VenuePair       domain.VenuePair `json:"venue_pair"`

	State           ExecState      `json:"state"`
	Leg1Fill        *Fill          `json:"leg_1_fill,omitempty"`
	Leg2Fill        *Fill          `json:"leg_2_fill,omitempty"`

	TargetNotional  float64              `json:"target_notional"`
	Leverage        domain.LeverageConfig `json:"leverage"`
	EntrySpread     float64        `json:"entry_spread"`
	CurrentSpread   float64        `json:"current_spread"`

	RiskTier        domain.RiskTier `json:"risk_tier"`
	HedgeMismatch   float64        `json:"hedge_mismatch"`
	CloseReason     CloseReason    `json:"close_reason,omitempty"`

	PricePnL        float64        `json:"price_pnl"`
	FundingPnL      float64        `json:"funding_pnl"`
	TotalPnL        float64        `json:"total_pnl"`
	RealizedPnL     float64        `json:"realized_pnl"`

	// Basis: relative price difference between legs.
	// Positive basis means leg1 price > leg2 price (relative to entry).
	EntryBasis      float64        `json:"entry_basis"`
	CurrentBasis    float64        `json:"current_basis"`
	BasisChange     float64        `json:"basis_change"`

	// Break-even estimate
	EstBreakEvenHours float64      `json:"est_break_even_hours"`
	BreakEvenReached  bool         `json:"break_even_reached"`
	HoldHours         float64      `json:"hold_hours"`

	Events          []Event        `json:"events"`

	CreatedAt       time.Time      `json:"created_at"`
	OpenedAt        *time.Time     `json:"opened_at,omitempty"`
	ClosedAt        *time.Time     `json:"closed_at,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func (p *Position) transition(to ExecState, reason string) {
	p.Events = append(p.Events, Event{
		FromState: p.State,
		ToState:   to,
		Reason:    reason,
		At:        time.Now(),
	})
	p.State = to
	p.UpdatedAt = time.Now()
}
