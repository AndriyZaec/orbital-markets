package live

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
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

type HyperliquidUnsignedLeverage struct {
	Action UpdateLeverageAction `json:"action"`
	Nonce  int64                `json:"nonce"`
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
	return BuildOpenPayloadWithBuilder(assetMap, symbol, side, amount, price, clientOrderID, nil)
}

func BuildOpenPayloadWithBuilder(
	assetMap AssetMap,
	symbol string,
	side domain.Side,
	amount float64,
	price float64,
	clientOrderID string,
	builder *BuilderCode,
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
		builder,
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
	return BuildClosePayloadWithBuilder(assetMap, symbol, positionSide, amount, price, clientOrderID, nil)
}

func BuildClosePayloadWithBuilder(
	assetMap AssetMap,
	symbol string,
	positionSide domain.Side,
	amount float64,
	price float64,
	clientOrderID string,
	builder *BuilderCode,
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
		builder,
	)
}

func BuildUpdateLeveragePayload(
	assetMap AssetMap,
	account, symbol string,
	leverage int,
) (*domain.SigningRequest, error) {
	assetIdx, ok := assetMap.AssetIndex(symbol)
	if !ok {
		return nil, fmt.Errorf("unknown asset: %s", symbol)
	}
	if leverage <= 0 {
		return nil, fmt.Errorf("invalid leverage: %d", leverage)
	}
	action := UpdateLeverageAction{Type: "updateLeverage", Asset: assetIdx, IsCross: true, Leverage: leverage}
	nonce := nextHyperliquidNonce()
	typedData, err := buildL1TypedData(action, nonce)
	if err != nil {
		return nil, fmt.Errorf("build Hyperliquid leverage typed data: %w", err)
	}
	unsignedBytes, err := json.Marshal(HyperliquidUnsignedLeverage{
		Action: action, Nonce: nonce, HyperliquidTypedData: typedData,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal leverage action: %w", err)
	}
	now := time.Now()
	id := fmt.Sprintf("hl-leverage-%s-%d", symbol, now.UnixNano())
	return &domain.SigningRequest{
		ID: id, ClientOrderID: id, Venue: "hyperliquid", Action: "update_leverage",
		Account: account, Symbol: symbol, Leverage: leverage, VenueAssetID: &assetIdx,
		UnsignedPayload: unsignedBytes, ExpiresAt: now.Add(30 * time.Second), CreatedAt: now,
	}, nil
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
	builder *BuilderCode,
) (*domain.SigningRequest, error) {
	assetIdx, ok := assetMap.AssetIndex(symbol)
	if !ok {
		return nil, fmt.Errorf("unknown asset: %s", symbol)
	}
	sizeDecimals, ok := assetMap.SizeDecimals(symbol)
	if !ok {
		return nil, fmt.Errorf("size precision unavailable for asset: %s", symbol)
	}
	normalizedAmount, amountWire, err := normalizeHyperliquidAmount(amount, sizeDecimals)
	if err != nil {
		return nil, fmt.Errorf("normalize %s amount: %w", symbol, err)
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
	limitPx, err := normalizeHyperliquidPrice(price*slippageMul, sizeDecimals)
	if err != nil {
		return nil, fmt.Errorf("normalize %s price: %w", symbol, err)
	}

	// cloid: 128-bit hex for Hyperliquid's client order tracking
	now := time.Now()
	cloid := fmt.Sprintf("0x%032x", now.UnixNano())

	orderAction := OrderAction{
		Type: "order",
		Orders: []OrderSpec{{
			Asset:      assetIdx,
			IsBuy:      isBuy,
			LimitPx:    limitPx,
			Size:       amountWire,
			ReduceOnly: reduceOnly,
			OrderType:  OrderType{Limit: LimitSpec{Tif: "Ioc"}},
			Cloid:      cloid,
		}},
		Grouping: "na",
		Builder:  builder,
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
		Amount:          normalizedAmount,
		Price:           price,
		ReduceOnly:      reduceOnly,
		VenueAssetID:    &assetIdx,
		UnsignedPayload: unsignedBytes,
		VenueMetadata:   metaBytes,
		ExpiresAt:       now.Add(30 * time.Second),
		CreatedAt:       now,
	}, nil
}

func normalizeHyperliquidAmount(amount float64, decimals int) (float64, string, error) {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, "", fmt.Errorf("invalid amount: %v", amount)
	}
	if decimals < 0 || decimals > 8 {
		return 0, "", fmt.Errorf("invalid size decimals: %d", decimals)
	}
	factor := math.Pow10(decimals)
	normalized := math.Floor(amount*factor+1e-9) / factor
	if normalized <= 0 {
		return 0, "", fmt.Errorf("amount is below minimum size precision")
	}
	wire := strconv.FormatFloat(normalized, 'f', decimals, 64)
	if strings.Contains(wire, ".") {
		wire = strings.TrimRight(strings.TrimRight(wire, "0"), ".")
	}
	return normalized, wire, nil
}

// NormalizeAmount rounds a base-asset amount down to Hyperliquid size precision.
func NormalizeAmount(assetMap AssetMap, symbol string, amount float64) (float64, error) {
	if assetMap == nil {
		return 0, fmt.Errorf("hyperliquid asset map not configured")
	}
	decimals, ok := assetMap.SizeDecimals(symbol)
	if !ok {
		return 0, fmt.Errorf("size precision unavailable for asset: %s", symbol)
	}
	normalized, _, err := normalizeHyperliquidAmount(amount, decimals)
	return normalized, err
}

func normalizeHyperliquidPrice(price float64, sizeDecimals int) (string, error) {
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return "", fmt.Errorf("invalid price: %v", price)
	}
	maxDecimals := 6 - sizeDecimals
	if maxDecimals < 0 || maxDecimals > 6 {
		return "", fmt.Errorf("invalid size decimals: %d", sizeDecimals)
	}
	decimalFactor := math.Pow10(maxDecimals)
	price = math.Round(price*decimalFactor) / decimalFactor
	significantDecimals := 4 - int(math.Floor(math.Log10(price)))
	if significantDecimals < maxDecimals {
		factor := math.Pow10(significantDecimals)
		price = math.Round(price*factor) / factor
		maxDecimals = max(significantDecimals, 0)
	}
	wire := strconv.FormatFloat(price, 'f', maxDecimals, 64)
	return strings.TrimRight(strings.TrimRight(wire, "0"), "."), nil
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

func buildL1TypedData(action any, nonce int64) (HyperliquidTypedData, error) {
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

func AttachLeverageSignature(signed domain.SignedAction, unsigned HyperliquidUnsignedLeverage) ([]byte, error) {
	signature, err := parseEthereumSignature(signed.Signature)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"action": unsigned.Action, "nonce": unsigned.Nonce, "signature": signature,
	})
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
