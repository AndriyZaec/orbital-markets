package live

import "time"

// MarketOrderRequest is the full message sent inside the WS envelope
// under params.create_market_order.
//
// Pacifica protocol reference (from SDK + docs):
//   - side: "bid" (long/buy) or "ask" (short/sell)
//   - amount: string, not float
//   - slippage_percent: string, not float
//   - client_order_id: full UUID string
//   - signature: base58-encoded Solana signMessage result
//
// The signed message is NOT this struct directly. See SigningMessage
// and BuildSigningMessage for the canonical signing payload.
type MarketOrderRequest struct {
	Account       string `json:"account"`
	Signature     string `json:"signature"`
	Timestamp     int64  `json:"timestamp"`
	ExpiryWindow  int64  `json:"expiry_window"`
	Symbol        string `json:"symbol"`
	Side          string `json:"side"` // "bid" or "ask"
	Amount        string `json:"amount"`
	ReduceOnly    bool   `json:"reduce_only"`
	SlippagePct   string `json:"slippage_percent"`
	ClientOrderID string `json:"client_order_id"`
}

// WSEnvelope is the top-level WebSocket message sent to Pacifica.
//
// Format: {"id": "uuid", "params": {"create_market_order": {...}}}
//
// The "id" is a client-defined request UUID for correlating responses.
// The response will echo this "id" back.
type WSEnvelope struct {
	ID     string         `json:"id"`
	Params map[string]any `json:"params"`
}

// SubmitResult is the structured outcome of an order submission attempt.
//
// Pacifica response format:
//
//	{"code": 200, "data": {"I": "cloid", "i": 645953, "s": "BTC"}, "id": "req-uuid", "t": 1749223025962, "type": "create_market_order"}
//
// code != 200 indicates rejection.
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
