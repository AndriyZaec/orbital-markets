package live

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const (
	defaultOpenSlippagePct  = 0.5 // 0.5% for open
	defaultCloseSlippagePct = 1.0 // 1.0% for close/unwind
)

// PacificaUnsignedOrder is the order payload the frontend must sign.
// This is the exact structure serialized to JSON for signing.
// Matches Pacifica's create_market_order WS message params,
// minus the signature field which the frontend will produce.
type PacificaUnsignedOrder struct {
	Account       string  `json:"account"`
	Timestamp     int64   `json:"timestamp"`
	ExpiryWindow  int64   `json:"expiry_window"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"` // "buy" or "sell"
	Amount        float64 `json:"amount"`
	ReduceOnly    bool    `json:"reduce_only"`
	SlippagePct   float64 `json:"slippage_percent"`
	ClientOrderID string  `json:"client_order_id"`
}

// PacificaSubmitMeta holds venue-specific metadata needed to submit
// the signed order but not included in the signed payload.
type PacificaSubmitMeta struct {
	WSURL  string `json:"ws_url"`
	Method string `json:"method"` // "create_market_order"
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
	pacSide := "buy"
	if side == domain.SideShort {
		pacSide = "sell"
	}

	return buildPayload(
		account,
		symbol,
		pacSide,
		amount,
		price,
		false,
		defaultOpenSlippagePct,
		clientOrderID,
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
	// Invert: close long = sell, close short = buy
	closeSide := "sell"
	if positionSide == domain.SideShort {
		closeSide = "buy"
	}

	return buildPayload(
		account,
		symbol,
		closeSide,
		amount,
		price,
		true,
		defaultCloseSlippagePct,
		clientOrderID,
		"close",
	)
}

func buildPayload(
	account string,
	symbol string,
	side string,
	amount float64,
	price float64,
	reduceOnly bool,
	slippagePct float64,
	clientOrderID string,
	action string,
) (*domain.SigningRequest, error) {
	now := time.Now()

	unsigned := PacificaUnsignedOrder{
		Account:       account,
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
		WSURL:  tradingWSURL,
		Method: "create_market_order",
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
		Amount:          amount,
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
		Account:       unsigned.Account,
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
