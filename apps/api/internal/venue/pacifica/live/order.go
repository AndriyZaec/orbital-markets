package live

import "time"

// MarketOrderRequest is the payload for a Pacifica market order.
type MarketOrderRequest struct {
	Account        string  `json:"account"`
	Signature      string  `json:"signature"`
	Timestamp      int64   `json:"timestamp"`
	ExpiryWindow   int64   `json:"expiry_window"`
	Symbol         string  `json:"symbol"`
	Side           string  `json:"side"` // "buy" or "sell"
	Amount         float64 `json:"amount"`
	ReduceOnly     bool    `json:"reduce_only"`
	SlippagePct    float64 `json:"slippage_percent"`
	ClientOrderID  string  `json:"client_order_id"`
}

// SubmitResult is the structured outcome of an order submission attempt.
type SubmitResult struct {
	// Identifiers
	RequestID     string `json:"request_id"`
	OrderID       string `json:"order_id"`
	ClientOrderID string `json:"client_order_id"`
	Symbol        string `json:"symbol"`

	// Outcome
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`

	// Timing
	SubmittedAt time.Time `json:"submitted_at"`
	RespondedAt time.Time `json:"responded_at"`
}
