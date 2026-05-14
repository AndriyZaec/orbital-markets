package executor

import "time"

// ExecState is the final state of a live execution attempt.
type ExecState string

const (
	ExecStateOpen     ExecState = "open"     // both legs filled, hedge intact
	ExecStateDegraded ExecState = "degraded" // hedge incomplete, leg 1 may be unwound
	ExecStateFailed   ExecState = "failed"   // nothing opened or fully unwound
)

// LegResult captures the outcome of one leg's live execution.
type LegResult struct {
	Venue         string  `json:"venue"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Submitted     bool    `json:"submitted"`
	Accepted      bool    `json:"accepted"`
	Filled        bool    `json:"filled"`
	FilledAmount  float64 `json:"filled_amount"`
	AvgFillPrice  float64 `json:"avg_fill_price"`
	RequestedAmt  float64 `json:"requested_amount"`
	FillRatio     float64 `json:"fill_ratio"`
	Fee           float64 `json:"fee"`
	OrderID       string  `json:"order_id"`
	ClientOrderID string  `json:"client_order_id"`
	Error         string  `json:"error,omitempty"`
}

// RecoveryAction records what recovery was attempted.
type RecoveryAction struct {
	Action    string `json:"action"` // "retry_leg2", "unwind_leg1", "none"
	Success   bool   `json:"success"`
	Detail    string `json:"detail,omitempty"`
}

// ExecutionResult is the full outcome of a two-leg live execution.
type ExecutionResult struct {
	OpportunityID string           `json:"opportunity_id"`
	PlanID        string           `json:"plan_id"`
	Asset         string           `json:"asset"`
	State         ExecState        `json:"state"`
	Leg1          LegResult        `json:"leg_1"`
	Leg2          LegResult        `json:"leg_2"`
	Recovery      []RecoveryAction `json:"recovery,omitempty"`
	Reasons       []string         `json:"reasons,omitempty"`
	StartedAt     time.Time        `json:"started_at"`
	CompletedAt   time.Time        `json:"completed_at"`
}
