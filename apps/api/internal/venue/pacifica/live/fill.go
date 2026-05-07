package live

import "time"

// OrderStatus represents the lifecycle states Orbital tracks for a Pacifica order.
type OrderStatus string

const (
	OrderStatusPending       OrderStatus = "pending"        // submitted, awaiting venue ack
	OrderStatusAccepted      OrderStatus = "accepted"       // venue accepted, awaiting fill
	OrderStatusPartialFill   OrderStatus = "partial_fill"   // some quantity filled
	OrderStatusFilled        OrderStatus = "filled"         // fully filled
	OrderStatusRejected      OrderStatus = "rejected"       // venue rejected
	OrderStatusCancelled     OrderStatus = "cancelled"      // cancelled (by user or system)
	OrderStatusExpired       OrderStatus = "expired"        // hit expiry window without fill
	OrderStatusTimeout       OrderStatus = "timeout"        // Orbital gave up waiting
)

// IsTerminal returns true if the order is in a final state.
func (s OrderStatus) IsTerminal() bool {
	switch s {
	case OrderStatusFilled, OrderStatusRejected, OrderStatusCancelled,
		OrderStatusExpired, OrderStatusTimeout:
		return true
	}
	return false
}

// IsFilled returns true if the order has any fill (partial or full).
func (s OrderStatus) IsFilled() bool {
	return s == OrderStatusFilled || s == OrderStatusPartialFill
}

// FillResult is the structured outcome of waiting for an order to fill.
type FillResult struct {
	// Identifiers
	OrderID       string `json:"order_id"`
	ClientOrderID string `json:"client_order_id"`
	Symbol        string `json:"symbol"`

	// Status
	Status OrderStatus `json:"status"`

	// Fill details
	RequestedAmount float64 `json:"requested_amount"`
	FilledAmount    float64 `json:"filled_amount"`
	AvgFillPrice    float64 `json:"avg_fill_price"`
	FillCount       int     `json:"fill_count"` // number of individual fills

	// Fees
	TotalFee float64 `json:"total_fee"`

	// Timing
	SubmittedAt time.Time  `json:"submitted_at"`
	FirstFillAt *time.Time `json:"first_fill_at,omitempty"`
	LastFillAt  *time.Time `json:"last_fill_at,omitempty"`
	ResolvedAt  time.Time  `json:"resolved_at"`

	// Error
	Error string `json:"error,omitempty"`
}

// FillRatio returns filled / requested.
func (r FillResult) FillRatio() float64 {
	if r.RequestedAmount <= 0 {
		return 0
	}
	return r.FilledAmount / r.RequestedAmount
}
