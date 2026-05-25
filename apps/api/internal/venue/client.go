package venue

import (
	"context"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

// OrderStatus represents the lifecycle states Orbital tracks for any venue order.
type OrderStatus string

const (
	OrderStatusPending     OrderStatus = "pending"
	OrderStatusAccepted    OrderStatus = "accepted"
	OrderStatusPartialFill OrderStatus = "partial_fill"
	OrderStatusFilled      OrderStatus = "filled"
	OrderStatusRejected    OrderStatus = "rejected"
	OrderStatusCancelled   OrderStatus = "cancelled"
	OrderStatusExpired     OrderStatus = "expired"
	OrderStatusTimeout     OrderStatus = "timeout"
)

func (s OrderStatus) IsTerminal() bool {
	switch s {
	case OrderStatusFilled, OrderStatusRejected, OrderStatusCancelled,
		OrderStatusExpired, OrderStatusTimeout:
		return true
	}
	return false
}

// SubmitResult is the venue-agnostic outcome of an order submission attempt.
type SubmitResult struct {
	Venue         string    `json:"venue"`
	OrderID       string    `json:"order_id"`
	ClientOrderID string    `json:"client_order_id"`
	Symbol        string    `json:"symbol"`
	Accepted      bool      `json:"accepted"`
	Error         string    `json:"error,omitempty"`
	SubmittedAt   time.Time `json:"submitted_at"`
	RespondedAt   time.Time `json:"responded_at"`
}

// FillResult is the venue-agnostic outcome of waiting for an order to fill.
type FillResult struct {
	Venue           string      `json:"venue"`
	OrderID         string      `json:"order_id"`
	ClientOrderID   string      `json:"client_order_id"`
	Symbol          string      `json:"symbol"`
	Status          OrderStatus `json:"status"`
	RequestedAmount float64     `json:"requested_amount"`
	FilledAmount    float64     `json:"filled_amount"`
	AvgFillPrice    float64     `json:"avg_fill_price"`
	FillCount       int         `json:"fill_count"`
	TotalFee        float64     `json:"total_fee"`
	SubmittedAt     time.Time   `json:"submitted_at"`
	FirstFillAt     *time.Time  `json:"first_fill_at,omitempty"`
	LastFillAt      *time.Time  `json:"last_fill_at,omitempty"`
	ResolvedAt      time.Time   `json:"resolved_at"`
	Error           string      `json:"error,omitempty"`
}

func (r FillResult) FillRatio() float64 {
	if r.RequestedAmount <= 0 {
		return 0
	}
	return r.FilledAmount / r.RequestedAmount
}

// OpenParams holds the parameters for opening a position leg.
type OpenParams struct {
	Symbol         string
	Side           domain.Side
	Amount         float64
	Price          float64
	Leverage       float64
	MarginRequired float64
	ClientOrderID  string
}

// CloseParams holds the parameters for closing/unwinding a position leg.
type CloseParams struct {
	Symbol        string
	PositionSide  domain.Side
	Amount        float64
	Price         float64
	ClientOrderID string
}

// VenueClient is the shared execution interface for all venue integrations.
// The executor depends on this interface rather than venue-specific clients.
type VenueClient interface {
	// Name returns the canonical venue identifier (e.g. "pacifica", "hyperliquid").
	Name() string

	// SubmitOpen submits a market order to open a position leg.
	SubmitOpen(ctx context.Context, params OpenParams) (*SubmitResult, error)

	// SubmitClose submits a reduce-only market order to close/unwind a position leg.
	SubmitClose(ctx context.Context, params CloseParams) (*SubmitResult, error)

	// WaitForFill blocks until the order reaches a terminal state or the context expires.
	WaitForFill(ctx context.Context, clientOrderID string) (*FillResult, error)
}
