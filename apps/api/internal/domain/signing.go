package domain

import (
	"encoding/json"
	"time"
)

// SigningRequest is what the backend sends to the frontend for signing.
// It contains the unsigned venue-specific payload and all correlation fields
// needed to track this action through the execution lifecycle.
//
// The frontend inspects the payload, signs it with the user's key,
// and returns a SignedAction.
type SigningRequest struct {
	// Correlation
	ID            string `json:"id"`              // unique request ID
	ClientOrderID string `json:"client_order_id"` // venue-facing order correlation

	// Venue context
	Venue  string `json:"venue"`  // "pacifica" or "hyperliquid"
	Action string `json:"action"` // "open" or "close"

	// Order fields (venue-agnostic summary for frontend display)
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"` // "buy" or "sell" (venue-native direction)
	Amount     float64 `json:"amount"`
	Price      float64 `json:"price"`       // reference price used for slippage calc
	ReduceOnly bool    `json:"reduce_only"` // true for close/unwind

	// Unsigned venue-specific payload — the exact bytes the frontend must sign.
	// Pacifica: JSON bytes of the order message.
	// Hyperliquid: JSON-encoded EIP-712 typed data.
	UnsignedPayload json.RawMessage `json:"unsigned_payload"`

	// Venue-specific metadata needed for submission but not part of the signed payload.
	// The frontend does not sign this — the backend uses it when submitting.
	VenueMetadata json.RawMessage `json:"venue_metadata,omitempty"`

	// Timing
	ExpiresAt time.Time `json:"expires_at"` // backend will reject after this
	CreatedAt time.Time `json:"created_at"`
}

// SignedAction is what the frontend returns after signing a SigningRequest.
// The backend uses this to submit the order to the venue.
type SignedAction struct {
	// Correlation — must match the SigningRequest.ID
	RequestID     string `json:"request_id"`
	ClientOrderID string `json:"client_order_id"`

	// Venue
	Venue string `json:"venue"`

	// Signer identity
	SignerAddress string `json:"signer_address"` // Solana pubkey or Ethereum address

	// The signature produced by the frontend.
	// Pacifica: base58 or hex signature over the unsigned payload bytes.
	// Hyperliquid: hex-encoded EIP-712 signature (r+s+v).
	Signature string `json:"signature"`
}
