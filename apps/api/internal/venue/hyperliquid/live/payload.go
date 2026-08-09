package live

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/vmihailenco/msgpack/v5"
	"golang.org/x/crypto/sha3"
)

const (
	defaultSlippageMulBuy  = 1.005 // 0.5% above for buy
	defaultSlippageMulSell = 0.995 // 0.5% below for sell
)

var lastHyperliquidNonce atomic.Int64

// HyperliquidUnsignedAction is the action payload the frontend must sign
// via EIP-712 typed data signing.
// This is the exact structure that maps to Hyperliquid's exchange action format.
type HyperliquidUnsignedAction struct {
	Action OrderAction `json:"action"`
	Nonce  int64       `json:"nonce"`
	HyperliquidTypedData
}

type EIP712Domain struct {
	ChainID           int    `json:"chainId"`
	Name              string `json:"name"`
	VerifyingContract string `json:"verifyingContract"`
	Version           string `json:"version"`
}

type EIP712Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type HyperliquidAgent struct {
	Source       string `json:"source"`
	ConnectionID string `json:"connectionId"`
}

type HyperliquidTypedData struct {
	Domain      EIP712Domain             `json:"domain"`
	Types       map[string][]EIP712Field `json:"types"`
	PrimaryType string                   `json:"primaryType"`
	Message     HyperliquidAgent         `json:"message"`
}

// HyperliquidSubmitMeta holds venue-specific metadata needed to submit
// the signed action but not included in the signed payload.
type HyperliquidSubmitMeta struct {
	ExchangeURL   string `json:"exchange_url"`
	Cloid         string `json:"cloid"`           // 128-bit hex client order ID for venue tracking
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

	nonce := nextHyperliquidNonce()

	typedData, err := buildL1TypedData(orderAction, nonce)
	if err != nil {
		return nil, fmt.Errorf("build Hyperliquid typed data: %w", err)
	}
	unsigned := HyperliquidUnsignedAction{
		Action:               orderAction,
		Nonce:                nonce,
		HyperliquidTypedData: typedData,
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

func nextHyperliquidNonce() int64 {
	for {
		previous := lastHyperliquidNonce.Load()
		next := time.Now().UnixMilli()
		if next <= previous {
			next = previous + 1
		}
		if lastHyperliquidNonce.CompareAndSwap(previous, next) {
			return next
		}
	}
}

func buildL1TypedData(action OrderAction, nonce int64) (HyperliquidTypedData, error) {
	var packed bytes.Buffer
	encoder := msgpack.NewEncoder(&packed)
	encoder.SetCustomStructTag("json")
	if err := encoder.Encode(action); err != nil {
		return HyperliquidTypedData{}, fmt.Errorf("msgpack action: %w", err)
	}
	if err := binary.Write(&packed, binary.BigEndian, uint64(nonce)); err != nil {
		return HyperliquidTypedData{}, fmt.Errorf("append nonce: %w", err)
	}
	packed.WriteByte(0) // No vault address.
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write(packed.Bytes())
	connectionID := "0x" + hex.EncodeToString(hash.Sum(nil))
	return HyperliquidTypedData{
		Domain: EIP712Domain{
			ChainID: 1337, Name: "Exchange", Version: "1",
			VerifyingContract: "0x0000000000000000000000000000000000000000",
		},
		Types: map[string][]EIP712Field{
			"Agent": {
				{Name: "source", Type: "string"},
				{Name: "connectionId", Type: "bytes32"},
			},
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
		},
		PrimaryType: "Agent",
		Message:     HyperliquidAgent{Source: "a", ConnectionID: connectionID},
	}, nil
}

// AttachSignature takes a signed action and produces the final request body
// ready for POST to the Hyperliquid exchange endpoint.
func AttachSignature(signed domain.SignedAction, unsigned HyperliquidUnsignedAction) ([]byte, error) {
	signature, err := parseEthereumSignature(signed.Signature)
	if err != nil {
		return nil, err
	}
	reqBody := map[string]any{
		"action":    unsigned.Action,
		"nonce":     unsigned.Nonce,
		"signature": signature,
	}
	return json.Marshal(reqBody)
}

type hyperliquidSignature struct {
	R string `json:"r"`
	S string `json:"s"`
	V int    `json:"v"`
}

func parseEthereumSignature(signature string) (hyperliquidSignature, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if err != nil || len(raw) != 65 {
		return hyperliquidSignature{}, fmt.Errorf("invalid Ethereum signature")
	}
	v := int(raw[64])
	if v < 27 {
		v += 27
	}
	if v != 27 && v != 28 {
		return hyperliquidSignature{}, fmt.Errorf("invalid Ethereum recovery id: %d", v)
	}
	return hyperliquidSignature{
		R: "0x" + hex.EncodeToString(raw[:32]),
		S: "0x" + hex.EncodeToString(raw[32:64]),
		V: v,
	}, nil
}
