package live

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster"
)

const (
	orderURL          = "https://fapi.asterdex.com/fapi/v3/order"
	signingRequestTTL = 30 * time.Second
	mainnetChainID    = 1666
	mainnetName       = "Mainnet"
	openSlippageBPS   = 50
	unwindSlippageBPS = 100
)

var (
	lastAsterNonce  atomic.Int64
	addressPattern  = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	clientIDPattern = regexp.MustCompile(`^[.A-Z:/a-z0-9_-]{1,36}$`)
	decimalPattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
)

type OrderRuleMap interface {
	OrderRules(symbol string) (aster.OrderRules, bool)
}

type BuilderConfig struct {
	Address string
	FeeRate string
}

type EIP712Domain struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	ChainID           int    `json:"chainId"`
	VerifyingContract string `json:"verifyingContract"`
}

type EIP712Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type EIP712Types struct {
	Domain  []EIP712Field `json:"EIP712Domain"`
	Message []EIP712Field `json:"Message"`
}

type AsterMessage struct {
	Msg string `json:"msg"`
}

// AsterUnsignedOrder contains the exact query string and EIP-712 typed data
// that the authorized Agent must sign.
type AsterUnsignedOrder struct {
	Domain      EIP712Domain `json:"domain"`
	Types       EIP712Types  `json:"types"`
	PrimaryType string       `json:"primaryType"`
	Message     AsterMessage `json:"message"`
}

type AsterSubmitMeta struct {
	OrderURL string `json:"order_url"`
}

func BuildOpenPayload(
	rules OrderRuleMap,
	user, signer, symbol string,
	side domain.Side,
	amount, price float64,
	clientOrderID string,
	builder *BuilderConfig,
) (*domain.SigningRequest, error) {
	if builder != nil {
		if err := validateBuilder(*builder); err != nil {
			return nil, err
		}
	}
	return buildPayload(
		rules, user, signer, symbol, side, amount, price, clientOrderID,
		false, "open", openSlippageBPS, builder, time.Now(), nextAsterNonce(),
	)
}

// BuildUnwindPayload builds a one-way-mode reduce-only order and does not
// charge a builder fee for recovering a partially opened hedge.
func BuildUnwindPayload(
	rules OrderRuleMap,
	user, signer, symbol string,
	positionSide domain.Side,
	amount, price float64,
	clientOrderID string,
) (*domain.SigningRequest, error) {
	orderSide, err := oppositeSide(positionSide)
	if err != nil {
		return nil, err
	}
	return buildPayload(
		rules, user, signer, symbol, orderSide, amount, price, clientOrderID,
		true, "unwind", unwindSlippageBPS, nil, time.Now(), nextAsterNonce(),
	)
}

func buildPayload(
	ruleMap OrderRuleMap,
	user, signer, symbol string,
	side domain.Side,
	amount, referencePrice float64,
	clientOrderID string,
	reduceOnly bool,
	action string,
	slippageBPS int64,
	builder *BuilderConfig,
	now time.Time,
	nonce int64,
) (*domain.SigningRequest, error) {
	if !addressPattern.MatchString(user) {
		return nil, fmt.Errorf("invalid Aster user address")
	}
	if !addressPattern.MatchString(signer) {
		return nil, fmt.Errorf("invalid Aster signer address")
	}
	if !clientIDPattern.MatchString(clientOrderID) {
		return nil, fmt.Errorf("invalid Aster client order ID")
	}
	if ruleMap == nil {
		return nil, fmt.Errorf("Aster order rules not configured")
	}
	rules, ok := ruleMap.OrderRules(symbol)
	if !ok {
		return nil, fmt.Errorf("Aster order rules unavailable for symbol: %s", symbol)
	}
	venueSide, err := sideToVenue(side)
	if err != nil {
		return nil, err
	}
	quantity, quantityWire, err := normalizeQuantity(amount, rules)
	if err != nil {
		return nil, fmt.Errorf("normalize %s quantity: %w", symbol, err)
	}
	_, priceWire, err := normalizePrice(referencePrice, side, slippageBPS, rules)
	if err != nil {
		return nil, fmt.Errorf("normalize %s price: %w", symbol, err)
	}
	if !reduceOnly {
		if err := validateNotional(quantityWire, priceWire, rules.MinNotional); err != nil {
			return nil, fmt.Errorf("validate %s order: %w", symbol, err)
		}
	}

	params := []queryParameter{{"symbol", symbol}, {"type", "LIMIT"}}
	if builder != nil {
		params = append(params, queryParameter{"builder", builder.Address}, queryParameter{"feeRate", builder.FeeRate})
	}
	params = append(params,
		queryParameter{"side", venueSide},
		queryParameter{"quantity", quantityWire},
		queryParameter{"price", priceWire},
		queryParameter{"timeInForce", "IOC"},
		queryParameter{"newClientOrderId", clientOrderID},
		queryParameter{"reduceOnly", strconv.FormatBool(reduceOnly)},
		queryParameter{"positionSide", "BOTH"},
		queryParameter{"asterChain", mainnetName},
		queryParameter{"user", user},
		queryParameter{"signer", signer},
		queryParameter{"nonce", strconv.FormatInt(nonce, 10)},
	)
	queryString := encodeQuery(params)
	unsigned := AsterUnsignedOrder{
		Domain: EIP712Domain{
			Name: "AsterSignTransaction", Version: "1", ChainID: mainnetChainID,
			VerifyingContract: "0x0000000000000000000000000000000000000000",
		},
		Types: EIP712Types{
			Domain: []EIP712Field{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			Message: []EIP712Field{{Name: "msg", Type: "string"}},
		},
		PrimaryType: "Message",
		Message:     AsterMessage{Msg: queryString},
	}
	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("marshal Aster unsigned order: %w", err)
	}
	metaBytes, err := json.Marshal(AsterSubmitMeta{OrderURL: orderURL})
	if err != nil {
		return nil, fmt.Errorf("marshal Aster venue metadata: %w", err)
	}

	return &domain.SigningRequest{
		ID:              fmt.Sprintf("aster-%s-%d", clientOrderID, now.UnixNano()),
		ClientOrderID:   clientOrderID,
		Venue:           "aster",
		Action:          action,
		Account:         user,
		Signer:          signer,
		Symbol:          symbol,
		Side:            strings.ToLower(venueSide),
		Amount:          quantity,
		Price:           referencePrice,
		ReduceOnly:      reduceOnly,
		UnsignedPayload: unsignedBytes,
		VenueMetadata:   metaBytes,
		ExpiresAt:       now.Add(signingRequestTTL),
		CreatedAt:       now,
	}, nil
}

type queryParameter struct {
	key   string
	value string
}

func encodeQuery(params []queryParameter) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, url.QueryEscape(param.key)+"="+url.QueryEscape(param.value))
	}
	return strings.Join(parts, "&")
}

func validateBuilder(builder BuilderConfig) error {
	if !addressPattern.MatchString(builder.Address) {
		return fmt.Errorf("invalid Aster builder address")
	}
	fee, err := parseDecimal("builder fee rate", builder.FeeRate)
	if err != nil || fee.Sign() <= 0 || fee.Cmp(big.NewRat(1, 1000)) > 0 {
		return fmt.Errorf("invalid Aster builder fee rate")
	}
	return nil
}

func sideToVenue(side domain.Side) (string, error) {
	switch side {
	case domain.SideLong:
		return "BUY", nil
	case domain.SideShort:
		return "SELL", nil
	default:
		return "", fmt.Errorf("invalid Aster side: %s", side)
	}
}

func oppositeSide(positionSide domain.Side) (domain.Side, error) {
	switch positionSide {
	case domain.SideLong:
		return domain.SideShort, nil
	case domain.SideShort:
		return domain.SideLong, nil
	default:
		return "", fmt.Errorf("invalid Aster position side: %s", positionSide)
	}
}

func normalizeQuantity(amount float64, rules aster.OrderRules) (float64, string, error) {
	quantity, wire, err := quantize(amount, rules.QuantityStep, false)
	if err != nil {
		return 0, "", err
	}
	if err := validateRange(wire, rules.MinQuantity, rules.MaxQuantity); err != nil {
		return 0, "", fmt.Errorf("quantity %w", err)
	}
	return quantity, wire, nil
}

func normalizePrice(price float64, side domain.Side, slippageBPS int64, rules aster.OrderRules) (float64, string, error) {
	if slippageBPS < 0 || slippageBPS >= 10_000 {
		return 0, "", fmt.Errorf("invalid slippage")
	}
	priceRat, err := floatRat("price", price)
	if err != nil {
		return 0, "", err
	}
	numerator := int64(10_000 - slippageBPS)
	roundUp := false
	if side == domain.SideLong {
		numerator = 10_000 + slippageBPS
		roundUp = true
	} else if side != domain.SideShort {
		return 0, "", fmt.Errorf("invalid side: %s", side)
	}
	target := new(big.Rat).Mul(priceRat, big.NewRat(numerator, 10_000))
	limitPrice, wire, err := quantizeRat(target, rules.TickSize, roundUp)
	if err != nil {
		return 0, "", err
	}
	if err := validateRange(wire, rules.MinPrice, rules.MaxPrice); err != nil {
		return 0, "", fmt.Errorf("price %w", err)
	}
	return limitPrice, wire, nil
}

func validateNotional(quantity, price, minimum string) error {
	quantityRat, err := parseDecimal("quantity", quantity)
	if err != nil {
		return err
	}
	priceRat, err := parseDecimal("price", price)
	if err != nil {
		return err
	}
	minimumRat, err := parseDecimal("minimum notional", minimum)
	if err != nil {
		return err
	}
	if new(big.Rat).Mul(quantityRat, priceRat).Cmp(minimumRat) < 0 {
		return fmt.Errorf("notional is below minimum %s", minimum)
	}
	return nil
}

func validateRange(value, minimum, maximum string) error {
	valueRat, err := parseDecimal("value", value)
	if err != nil {
		return err
	}
	minimumRat, err := parseDecimal("minimum", minimum)
	if err != nil {
		return err
	}
	maximumRat, err := parseDecimal("maximum", maximum)
	if err != nil {
		return err
	}
	if valueRat.Cmp(minimumRat) < 0 || valueRat.Cmp(maximumRat) > 0 {
		return fmt.Errorf("%s is outside [%s, %s]", value, minimum, maximum)
	}
	return nil
}

func quantize(value float64, increment string, roundUp bool) (float64, string, error) {
	valueRat, err := floatRat("value", value)
	if err != nil {
		return 0, "", err
	}
	return quantizeRat(valueRat, increment, roundUp)
}

func quantizeRat(value *big.Rat, increment string, roundUp bool) (float64, string, error) {
	step, err := parseDecimal("increment", increment)
	if err != nil || step.Sign() <= 0 {
		return 0, "", fmt.Errorf("invalid increment: %s", increment)
	}
	quotient := new(big.Rat).Quo(value, step)
	tolerance := big.NewRat(1, 1_000_000_000)
	if roundUp {
		quotient.Sub(quotient, tolerance)
	} else {
		quotient.Add(quotient, tolerance)
	}
	units, remainder := new(big.Int), new(big.Int)
	units.QuoRem(quotient.Num(), quotient.Denom(), remainder)
	if roundUp && remainder.Sign() != 0 {
		units.Add(units, big.NewInt(1))
	}
	result := new(big.Rat).Mul(new(big.Rat).SetInt(units), step)
	wire := trimDecimal(result.FloatString(decimalPlaces(increment)))
	normalized, err := strconv.ParseFloat(wire, 64)
	if err != nil || !isFinitePositive(normalized) {
		return 0, "", fmt.Errorf("normalized value is not positive")
	}
	return normalized, wire, nil
}

func floatRat(name string, value float64) (*big.Rat, error) {
	if !isFinitePositive(value) {
		return nil, fmt.Errorf("%s must be positive", name)
	}
	return parseDecimal(name, strconv.FormatFloat(value, 'f', -1, 64))
}

func parseDecimal(name, value string) (*big.Rat, error) {
	if !decimalPattern.MatchString(value) {
		return nil, fmt.Errorf("invalid %s: %s", name, value)
	}
	parsed, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("invalid %s: %s", name, value)
	}
	return parsed, nil
}

func decimalPlaces(value string) int {
	dot := strings.IndexByte(value, '.')
	if dot < 0 {
		return 0
	}
	return len(value) - dot - 1
}

func trimDecimal(value string) string {
	if !strings.Contains(value, ".") {
		return value
	}
	value = strings.TrimRight(value, "0")
	return strings.TrimSuffix(value, ".")
}

func isFinitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func nextAsterNonce() int64 {
	for {
		previous := lastAsterNonce.Load()
		next := time.Now().UnixMicro()
		if next <= previous {
			next = previous + 1
		}
		if lastAsterNonce.CompareAndSwap(previous, next) {
			return next
		}
	}
}
