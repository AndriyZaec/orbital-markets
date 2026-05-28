package live

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const (
	defaultOpenSlippagePct  = "0.5" // 0.5% for open
	defaultCloseSlippagePct = "1.0" // 1.0% for close/unwind
)

// PacificaUnsignedOrder is the order payload the frontend must sign.
//
// Signing protocol:
//  1. Build header: {"timestamp": ms, "expiry_window": ms, "type": "create_market_order"}
//  2. Build data: {"symbol", "side", "amount", "reduce_only", "slippage_percent", "client_order_id"}
//  3. Merge: {header..., "data": data}
//  4. Sort keys recursively, compact JSON, UTF-8 encode
//  5. Sign with Solana signMessage → base58 signature
//
// The frontend receives this struct as UnsignedPayload, constructs the
// canonical signing message using the same algorithm, and returns a base58 signature.
type PacificaUnsignedOrder struct {
	Timestamp     int64  `json:"timestamp"`
	ExpiryWindow  int64  `json:"expiry_window"`
	Symbol        string `json:"symbol"`
	Side          string `json:"side"` // "bid" or "ask"
	Amount        string `json:"amount"`
	ReduceOnly    bool   `json:"reduce_only"`
	SlippagePct   string `json:"slippage_percent"`
	ClientOrderID string `json:"client_order_id"`
}

// PacificaSubmitMeta holds venue-specific metadata needed to submit
// the signed order but not included in the signed payload.
type PacificaSubmitMeta struct {
	WSURL      string `json:"ws_url"`
	ActionType string `json:"action_type"` // "create_market_order"
}

// BuildOpenPayload constructs an unsigned signing request for a Pacifica open order.
func BuildOpenPayload(
	account string,
	symbol string,
	side domain.Side,
	amount float64,
	price float64,
	clientOrderID string,
) (*domain.SigningRequest, error) {
	return buildPayload(
		symbol,
		sideToVenue(side),
		fmt.Sprintf("%g", amount),
		price,
		false,
		defaultOpenSlippagePct,
		ensureUUID(clientOrderID),
		"open",
	)
}

// BuildClosePayload constructs an unsigned signing request for a Pacifica close order.
// Side is the position side — it will be inverted for the close order.
func BuildClosePayload(
	account string,
	symbol string,
	positionSide domain.Side,
	amount float64,
	price float64,
	clientOrderID string,
) (*domain.SigningRequest, error) {
	// Invert: close long = ask, close short = bid
	closeSide := "ask"
	if positionSide == domain.SideShort {
		closeSide = "bid"
	}

	return buildPayload(
		symbol,
		closeSide,
		fmt.Sprintf("%g", amount),
		price,
		true,
		defaultCloseSlippagePct,
		ensureUUID(clientOrderID),
		"close",
	)
}

func buildPayload(
	symbol string,
	side string,
	amount string,
	price float64,
	reduceOnly bool,
	slippagePct string,
	clientOrderID string,
	action string,
) (*domain.SigningRequest, error) {
	now := time.Now()

	unsigned := PacificaUnsignedOrder{
		Timestamp:     now.UnixMilli(),
		ExpiryWindow:  expiryWindowMs,
		Symbol:        symbol,
		Side:          side,
		Amount:        amount,
		ReduceOnly:    reduceOnly,
		SlippagePct:   slippagePct,
		ClientOrderID: clientOrderID,
	}

	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("marshal unsigned order: %w", err)
	}

	meta := PacificaSubmitMeta{
		WSURL:      tradingWSURL,
		ActionType: "create_market_order",
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal venue metadata: %w", err)
	}

	return &domain.SigningRequest{
		ID:              fmt.Sprintf("pac-%s-%d", clientOrderID, now.UnixNano()),
		ClientOrderID:   clientOrderID,
		Venue:           "pacifica",
		Action:          action,
		Symbol:          symbol,
		Side:            side,
		Amount:          0, // not used for display in non-custodial flow
		Price:           price,
		ReduceOnly:      reduceOnly,
		UnsignedPayload: unsignedBytes,
		VenueMetadata:   metaBytes,
		ExpiresAt:       now.Add(30 * time.Second),
		CreatedAt:       now,
	}, nil
}

// AttachSignature takes a signed action and produces the final MarketOrderRequest
// ready for WS submission.
func AttachSignature(
	unsigned PacificaUnsignedOrder,
	signed domain.SignedAction,
) MarketOrderRequest {
	return MarketOrderRequest{
		Account:       signed.SignerAddress,
		Signature:     signed.Signature,
		Timestamp:     unsigned.Timestamp,
		ExpiryWindow:  unsigned.ExpiryWindow,
		Symbol:        unsigned.Symbol,
		Side:          unsigned.Side,
		Amount:        unsigned.Amount,
		ReduceOnly:    unsigned.ReduceOnly,
		SlippagePct:   unsigned.SlippagePct,
		ClientOrderID: unsigned.ClientOrderID,
	}
}

// sideToVenue is duplicated from client.go to avoid circular dependency.
// Both files are in the same package so this is just a local alias.
// Keeping one definition — using the one in client.go.
// This file uses the function defined in client.go.
