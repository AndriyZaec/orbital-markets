package domain

import "time"

type PositionState string

const (
	StatePendingOpen  PositionState = "pending_open"
	StateOpening      PositionState = "opening"
	StateOpen         PositionState = "open"
	StateDegradedOpen PositionState = "degraded_open"
	StatePendingClose PositionState = "pending_close"
	StateClosing      PositionState = "closing"
	StateClosed       PositionState = "closed"
	StateFailed       PositionState = "failed"
)

type Position struct {
	ID            string        `json:"id"`
	OpportunityID string        `json:"opportunity_id"`
	State         PositionState `json:"state"`
	Asset         string        `json:"asset"`
	VenuePair     VenuePair     `json:"venue_pair"`
	Direction     Direction     `json:"direction"`
	Notional      float64       `json:"notional"`
	EntrySpread   float64       `json:"entry_spread"`
	OpenedAt      time.Time     `json:"opened_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	Warnings      []string      `json:"warnings,omitempty"`
}
