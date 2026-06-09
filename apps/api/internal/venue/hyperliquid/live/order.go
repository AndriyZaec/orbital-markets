package live

import "time"

// OrderAction is the Hyperliquid exchange action for placing orders.
// Hyperliquid uses a POST /exchange endpoint with EIP-712 typed signing.
type OrderAction struct {
	Type      string      `json:"type"` // "order"
	Orders    []OrderSpec `json:"orders"`
	Grouping  string      `json:"grouping"` // "na" for single orders
}

// OrderSpec is a single order within an action.
type OrderSpec struct {
	Asset      int       `json:"a"`           // asset index from meta.universe
	IsBuy      bool      `json:"b"`           // true = buy, false = sell
	LimitPx    string    `json:"p"`           // price as string
	Size       string    `json:"s"`           // size as string
	ReduceOnly bool      `json:"r"`           // true for close/unwind
	OrderType  OrderType `json:"t"`
	Cloid      string    `json:"c,omitempty"` // client order ID, 128-bit hex
}

// OrderType specifies market vs limit behavior.
type OrderType struct {
	Limit LimitSpec `json:"limit"`
}

// LimitSpec configures the order fill behavior.
type LimitSpec struct {
	Tif string `json:"tif"` // "Ioc" for market-like behavior
}

// SubmitResult is the structured outcome of a Hyperliquid order submission.
type SubmitResult struct {
	RequestID     string    `json:"request_id"`
	OrderID       string    `json:"order_id"`
	ClientOrderID string    `json:"client_order_id"`
	Symbol        string    `json:"symbol"`
	Accepted      bool      `json:"accepted"`
	Error         string    `json:"error,omitempty"`
	SubmittedAt   time.Time `json:"submitted_at"`
	RespondedAt   time.Time `json:"responded_at"`
}

// OrderStatus represents the lifecycle states Orbital tracks for a Hyperliquid order.
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

// FillResult is the structured outcome of waiting for an order to fill.
type FillResult struct {
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
