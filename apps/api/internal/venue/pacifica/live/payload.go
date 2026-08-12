package live

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const (
	defaultOpenSlippagePct  = "0.5" // 0.5% for open
	defaultCloseSlippagePct = "1.0" // 1.0% for close/unwind
	signingRequestTTL       = 30 * time.Second
)

type LotSizeMap interface {
	LotSize(symbol string) (string, bool)
}

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
	lotSizes LotSizeMap,
	account string,
	symbol string,
	side domain.Side,
	amount float64,
	price float64,
	clientOrderID string,
) (*domain.SigningRequest, error) {
	return buildPayload(
		lotSizes,
		account,
		symbol,
		sideToVenue(side),
		amount,
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
	lotSizes LotSizeMap,
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
		lotSizes,
		account,
		symbol,
		closeSide,
		amount,
		price,
		true,
		defaultCloseSlippagePct,
		ensureUUID(clientOrderID),
		"close",
	)
}

func buildPayload(
	lotSizes LotSizeMap,
	account string,
	symbol string,
	side string,
	amount float64,
	price float64,
	reduceOnly bool,
	slippagePct string,
	clientOrderID string,
	action string,
) (*domain.SigningRequest, error) {
	now := time.Now()
	normalizedAmount, amountWire, err := normalizePacificaAmount(lotSizes, symbol, amount)
	if err != nil {
		return nil, err
	}

	unsigned := PacificaUnsignedOrder{
		Timestamp:     now.UnixMilli(),
		ExpiryWindow:  expiryWindowMs,
		Symbol:        symbol,
		Side:          side,
		Amount:        amountWire,
		ReduceOnly:    reduceOnly,
		SlippagePct:   slippagePct,
		ClientOrderID: clientOrderID,
	}
	requestExpiresAt := time.UnixMilli(unsigned.Timestamp).Add(signingRequestTTL)

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
		Account:         account,
		Symbol:          symbol,
		Side:            side,
		Amount:          normalizedAmount,
		Price:           price,
		ReduceOnly:      reduceOnly,
		UnsignedPayload: unsignedBytes,
		VenueMetadata:   metaBytes,
		ExpiresAt:       requestExpiresAt,
		CreatedAt:       now,
	}, nil
}

func normalizePacificaAmount(lotSizes LotSizeMap, symbol string, amount float64) (float64, string, error) {
	if lotSizes == nil {
		return 0, "", fmt.Errorf("pacifica lot sizes not configured")
	}
	lotSizeWire, ok := lotSizes.LotSize(symbol)
	if !ok {
		return 0, "", fmt.Errorf("lot size unavailable for asset: %s", symbol)
	}
	lotSize, err := strconv.ParseFloat(lotSizeWire, 64)
	if err != nil || lotSize <= 0 || math.IsInf(lotSize, 0) || math.IsNaN(lotSize) {
		return 0, "", fmt.Errorf("invalid lot size for asset: %s", symbol)
	}
	decimals := 0
	if dot := strings.IndexByte(lotSizeWire, '.'); dot >= 0 {
		decimals = len(strings.TrimRight(lotSizeWire[dot+1:], "0"))
	}
	units := math.Floor((amount / lotSize) + 1e-9)
	normalized := units * lotSize
	if normalized <= 0 || math.IsInf(normalized, 0) || math.IsNaN(normalized) {
		return 0, "", fmt.Errorf("amount is below Pacifica lot size for asset: %s", symbol)
	}
	wire := strconv.FormatFloat(normalized, 'f', decimals, 64)
	parsed, err := strconv.ParseFloat(wire, 64)
	if err != nil {
		return 0, "", fmt.Errorf("format amount for asset %s: %w", symbol, err)
	}
	return parsed, wire, nil
}

// AttachSignature takes a signed action and produces the final MarketOrderRequest
// ready for WS submission.
func AttachSignature(
	unsigned PacificaUnsignedOrder,
	signed domain.SignedAction,
	request *domain.SigningRequest,
) MarketOrderRequest {
	return MarketOrderRequest{
		Account:       request.Account,
		AgentWallet:   request.Signer,
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
