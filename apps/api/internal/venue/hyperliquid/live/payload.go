package live

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const (
	defaultSlippageMulBuy  = 1.005 // 0.5% above for buy
	defaultSlippageMulSell = 0.995 // 0.5% below for sell
)

// HyperliquidUnsignedAction is the action payload the frontend must sign
// via EIP-712 typed data signing.
// This is the exact structure that maps to Hyperliquid's exchange action format.
type HyperliquidUnsignedAction struct {
	Action OrderAction `json:"action"`
	Nonce  int64       `json:"nonce"`
}

// HyperliquidSubmitMeta holds venue-specific metadata needed to submit
// the signed action but not included in the signed payload.
type HyperliquidSubmitMeta struct {
	ExchangeURL   string `json:"exchange_url"`
	Cloid         string `json:"cloid"`          // 128-bit hex client order ID for venue tracking
	ClientOrderID string `json:"client_order_id"` // Orbital-side correlation
}

// BuildOpenPayload constructs an unsigned signing request for a Hyperliquid open order.
func BuildOpenPayload(
	assetMap AssetMap,
	symbol string,
	side domain.Side,
	amount float64,
	price float64,
	clientOrderID string,
) (*domain.SigningRequest, error) {
	return buildPayload(
		assetMap,
		symbol,
		side,
		amount,
		price,
		false,
		clientOrderID,
		"open",
	)
}

// BuildClosePayload constructs an unsigned signing request for a Hyperliquid close order.
// Side is the position side — it will be inverted for the close order.
func BuildClosePayload(
	assetMap AssetMap,
	symbol string,
	positionSide domain.Side,
	amount float64,
	price float64,
	clientOrderID string,
) (*domain.SigningRequest, error) {
	// Invert: close long = sell, close short = buy
	closeSide := domain.SideLong
	if positionSide == domain.SideLong {
		closeSide = domain.SideShort
	}

	return buildPayload(
		assetMap,
		symbol,
		closeSide,
		amount,
		price,
		true,
		clientOrderID,
		"close",
	)
}

func buildPayload(
	assetMap AssetMap,
	symbol string,
	side domain.Side,
	amount float64,
	price float64,
	reduceOnly bool,
	clientOrderID string,
	action string,
) (*domain.SigningRequest, error) {
	assetIdx, ok := assetMap.AssetIndex(symbol)
	if !ok {
		return nil, fmt.Errorf("unknown asset: %s", symbol)
	}

	isBuy := side == domain.SideLong
	venueSide := "sell"
	if isBuy {
		venueSide = "buy"
	}

	// IOC limit price with slippage
	slippageMul := defaultSlippageMulSell
	if isBuy {
		slippageMul = defaultSlippageMulBuy
	}
	limitPx := fmt.Sprintf("%.6f", price*slippageMul)

	// cloid: 128-bit hex for Hyperliquid's client order tracking
	now := time.Now()
	cloid := fmt.Sprintf("0x%032x", now.UnixNano())

	orderAction := OrderAction{
		Type: "order",
		Orders: []OrderSpec{{
			Asset:      assetIdx,
			IsBuy:      isBuy,
			LimitPx:    limitPx,
			Size:       fmt.Sprintf("%.6f", amount),
			ReduceOnly: reduceOnly,
			OrderType:  OrderType{Limit: LimitSpec{Tif: "Ioc"}},
			Cloid:      cloid,
		}},
		Grouping: "na",
	}

	nonce := now.UnixMilli()

	unsigned := HyperliquidUnsignedAction{
		Action: orderAction,
		Nonce:  nonce,
	}

	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("marshal unsigned action: %w", err)
	}

	meta := HyperliquidSubmitMeta{
		ExchangeURL:   exchangeURL,
		Cloid:         cloid,
		ClientOrderID: clientOrderID,
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal venue metadata: %w", err)
	}

	return &domain.SigningRequest{
		ID:              fmt.Sprintf("hl-%s-%d", clientOrderID, now.UnixNano()),
		ClientOrderID:   clientOrderID,
		Venue:           "hyperliquid",
		Action:          action,
		Symbol:          symbol,
		Side:            venueSide,
		Amount:          amount,
		Price:           price,
		ReduceOnly:      reduceOnly,
		UnsignedPayload: unsignedBytes,
		VenueMetadata:   metaBytes,
		ExpiresAt:       now.Add(30 * time.Second),
		CreatedAt:       now,
	}, nil
}

// AttachSignature takes a signed action and produces the final request body
// ready for POST to the Hyperliquid exchange endpoint.
func AttachSignature(signed domain.SignedAction, unsigned HyperliquidUnsignedAction) ([]byte, error) {
	reqBody := map[string]any{
		"action":    unsigned.Action,
		"nonce":     unsigned.Nonce,
		"signature": signed.Signature,
	}
	return json.Marshal(reqBody)
}
