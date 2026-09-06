package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const (
	orderSubmitTimeout = 10 * time.Second
	maxOrderResponse   = 64 << 10
)

var (
	ErrSubmissionAmbiguous = errors.New("Aster submission outcome is unknown")
	ErrSubmissionNotSent   = errors.New("Aster submission was not sent")
)

type FillResult struct {
	OrderID       string
	ClientOrderID string
	Status        string
	FilledAmount  float64
	AvgFillPrice  float64
	Filled        bool
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
	fillsMu    sync.RWMutex
	fills      map[string]FillResult
}

func NewClient(baseURL string, httpClient *http.Client, logger *slog.Logger) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: orderSubmitTimeout}
	}
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), httpClient: &client, logger: logger,
		fills: make(map[string]FillResult),
	}
}

func NewDefaultClient(logger *slog.Logger) *Client {
	return NewClient(asterAPIBaseURL, &http.Client{Timeout: orderSubmitTimeout}, logger)
}

// SubmitSignedOrder relays an already-authorized browser-agent signature.
// Message.msg is transmitted byte-for-byte with signature appended last.
func (c *Client) SubmitSignedOrder(
	ctx context.Context,
	signed domain.SignedAction,
	request *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	query, err := validateSignedOrder(signed, request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSubmissionNotSent, err)
	}
	body := query + "&signature=" + url.QueryEscape(signed.Signature)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/fapi/v3/order", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: build order request: %v", ErrSubmissionNotSent, err)
	}
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	submittedAt := time.Now()
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSubmissionAmbiguous, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxOrderResponse+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrSubmissionAmbiguous, err)
	}
	if len(responseBody) > maxOrderResponse {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrSubmissionAmbiguous, maxOrderResponse)
	}
	respondedAt := time.Now()
	if response.StatusCode >= http.StatusInternalServerError {
		return nil, fmt.Errorf("%w: HTTP %d", ErrSubmissionAmbiguous, response.StatusCode)
	}

	result := &domain.SubmissionResult{
		RequestID: signed.RequestID, ClientOrderID: request.ClientOrderID, Venue: "aster",
		SubmittedAt: submittedAt, RespondedAt: respondedAt,
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		result.Error = parseAsterOrderError(response.StatusCode, responseBody)
		return result, nil
	}

	var venueResult struct {
		Code          int             `json:"code"`
		Message       string          `json:"msg"`
		OrderID       json.RawMessage `json:"orderId"`
		ClientOrderID string          `json:"clientOrderId"`
		Symbol        string          `json:"symbol"`
		Status        string          `json:"status"`
		ExecutedQty   string          `json:"executedQty"`
		CumQuote      string          `json:"cumQuote"`
		AvgPrice      string          `json:"avgPrice"`
		OrigQty       string          `json:"origQty"`
		Price         string          `json:"price"`
		Side          string          `json:"side"`
		PositionSide  string          `json:"positionSide"`
		TimeInForce   string          `json:"timeInForce"`
		Type          string          `json:"type"`
		ReduceOnly    *bool           `json:"reduceOnly"`
	}
	if err := json.Unmarshal(responseBody, &venueResult); err != nil {
		return nil, fmt.Errorf("%w: decode successful response: %v", ErrSubmissionAmbiguous, err)
	}
	if venueResult.Code != 0 && venueResult.Code != http.StatusOK {
		result.Error = formatAsterError(venueResult.Code, venueResult.Message)
		return result, nil
	}
	orderID, err := parseAsterOrderID(venueResult.OrderID)
	executedQty, quantityErr := strconv.ParseFloat(venueResult.ExecutedQty, 64)
	avgPrice, priceErr := parseAsterAveragePrice(venueResult.AvgPrice, venueResult.CumQuote, executedQty)
	origQty, origQtyErr := strconv.ParseFloat(venueResult.OrigQty, 64)
	limitPrice, limitPriceErr := strconv.ParseFloat(venueResult.Price, 64)
	if err != nil || quantityErr != nil || priceErr != nil || origQtyErr != nil || limitPriceErr != nil ||
		venueResult.ClientOrderID != request.ClientOrderID || venueResult.Symbol != request.Symbol ||
		venueResult.Side != strings.ToUpper(request.Side) || venueResult.PositionSide != "BOTH" ||
		venueResult.TimeInForce != "IOC" || venueResult.Type != "LIMIT" ||
		venueResult.ReduceOnly == nil || *venueResult.ReduceOnly != request.ReduceOnly ||
		origQty != request.Amount || limitPrice != request.Price ||
		!isFiniteNonNegative(executedQty) || executedQty > request.Amount ||
		!isFiniteNonNegative(avgPrice) || (executedQty > 0 && avgPrice == 0) {
		return nil, fmt.Errorf("%w: response correlation mismatch", ErrSubmissionAmbiguous)
	}
	switch venueResult.Status {
	case "FILLED":
		if executedQty != request.Amount {
			return nil, fmt.Errorf("%w: filled order quantity mismatch", ErrSubmissionAmbiguous)
		}
		result.Accepted = true
	case "EXPIRED":
		if executedQty == request.Amount {
			return nil, fmt.Errorf("%w: expired order reports a full fill", ErrSubmissionAmbiguous)
		}
		result.Accepted = true
	case "REJECTED":
		result.Error = "Aster rejected the order"
		return result, nil
	default:
		return nil, fmt.Errorf("%w: unrecognized order status %q", ErrSubmissionAmbiguous, venueResult.Status)
	}
	result.OrderID = orderID
	fillStatus := strings.ToLower(venueResult.Status)
	if venueResult.Status == "EXPIRED" && executedQty > 0 {
		fillStatus = "partial_fill"
	}
	c.fillsMu.Lock()
	c.fills[asterFillKey(request.Account, request.ClientOrderID)] = FillResult{
		OrderID: orderID, ClientOrderID: request.ClientOrderID, Status: fillStatus,
		FilledAmount: executedQty, AvgFillPrice: avgPrice, Filled: executedQty > 0,
	}
	c.fillsMu.Unlock()

	if c.logger != nil {
		c.logger.Info("aster live: signed order response",
			"client_order_id", request.ClientOrderID,
			"order_id", orderID,
			"status", venueResult.Status,
		)
	}
	return result, nil
}

func (c *Client) WaitForFill(_ context.Context, account, clientOrderID string) (*FillResult, error) {
	key := asterFillKey(account, clientOrderID)
	c.fillsMu.Lock()
	fill, ok := c.fills[key]
	delete(c.fills, key)
	c.fillsMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("Aster fill result unavailable for %s", clientOrderID)
	}
	return &fill, nil
}

func asterFillKey(account, clientOrderID string) string {
	return strings.ToLower(account) + "|" + clientOrderID
}

func validateSignedOrder(signed domain.SignedAction, request *domain.SigningRequest) (string, error) {
	if request == nil || request.Venue != "aster" || signed.Venue != "aster" {
		return "", fmt.Errorf("invalid Aster signing context")
	}
	if time.Now().After(request.ExpiresAt) {
		return "", fmt.Errorf("Aster signing request expired")
	}
	if signed.RequestID != request.ID || signed.ClientOrderID != request.ClientOrderID ||
		!strings.EqualFold(signed.SignerAddress, request.Signer) {
		return "", fmt.Errorf("Aster signed action correlation mismatch")
	}
	if err := validateEthereumSignature(signed.Signature); err != nil {
		return "", err
	}
	var metadata AsterSubmitMeta
	if err := json.Unmarshal(request.VenueMetadata, &metadata); err != nil || metadata.OrderURL != orderURL {
		return "", fmt.Errorf("invalid Aster order endpoint metadata")
	}
	var unsigned AsterUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		return "", fmt.Errorf("decode Aster unsigned order: %w", err)
	}
	if !validAsterTypedData(unsigned) {
		return "", fmt.Errorf("invalid Aster typed-data envelope")
	}
	values, err := url.ParseQuery(unsigned.Message.Msg)
	if err != nil || validateAsterOrderQuery(values, request) != nil {
		return "", fmt.Errorf("invalid Aster signed order query")
	}
	return unsigned.Message.Msg, nil
}

func equalEIP712Fields(actual, expected []EIP712Field) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func validateAsterOrderQuery(values url.Values, request *domain.SigningRequest) error {
	allowed := map[string]bool{
		"symbol": true, "type": true, "side": true, "quantity": true, "price": true,
		"timeInForce": true, "newClientOrderId": true, "newOrderRespType": true,
		"reduceOnly": true, "positionSide": true, "asterChain": true, "user": true,
		"signer": true, "nonce": true,
	}
	for key, value := range values {
		if !allowed[key] || len(value) != 1 {
			return fmt.Errorf("unexpected or duplicate parameter")
		}
	}
	if len(values) != 14 {
		return fmt.Errorf("unexpected parameter count")
	}
	if values.Get("symbol") != request.Symbol || values.Get("type") != "LIMIT" ||
		values.Get("side") != strings.ToUpper(request.Side) || values.Get("timeInForce") != "IOC" ||
		values.Get("newClientOrderId") != request.ClientOrderID || values.Get("newOrderRespType") != "RESULT" ||
		values.Get("reduceOnly") != strconv.FormatBool(request.ReduceOnly) || values.Get("positionSide") != "BOTH" ||
		values.Get("asterChain") != mainnetName || !strings.EqualFold(values.Get("user"), request.Account) ||
		!strings.EqualFold(values.Get("signer"), request.Signer) {
		return fmt.Errorf("order summary mismatch")
	}
	quantity, quantityErr := strconv.ParseFloat(values.Get("quantity"), 64)
	price, priceErr := strconv.ParseFloat(values.Get("price"), 64)
	nonce, nonceErr := strconv.ParseInt(values.Get("nonce"), 10, 64)
	if quantityErr != nil || priceErr != nil || nonceErr != nil ||
		!decimalPattern.MatchString(values.Get("quantity")) || !decimalPattern.MatchString(values.Get("price")) ||
		quantity != request.Amount || price != request.Price || nonce < request.CreatedAt.UnixMicro() ||
		nonce > request.ExpiresAt.UnixMicro() {
		return fmt.Errorf("invalid numeric parameter")
	}
	return nil
}

func parseAsterAveragePrice(value, cumulativeQuote string, filledAmount float64) (float64, error) {
	average, err := strconv.ParseFloat(value, 64)
	if err == nil && !math.IsNaN(average) && !math.IsInf(average, 0) && (average > 0 || filledAmount == 0) {
		return average, nil
	}
	quote, quoteErr := strconv.ParseFloat(cumulativeQuote, 64)
	if quoteErr != nil || filledAmount <= 0 || !isFiniteNonNegative(quote) || quote == 0 {
		return 0, fmt.Errorf("invalid Aster average fill price")
	}
	average = quote / filledAmount
	if !isFiniteNonNegative(average) || average == 0 {
		return 0, fmt.Errorf("invalid Aster average fill price")
	}
	return average, nil
}

func isFiniteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func parseAsterOrderID(raw json.RawMessage) (string, error) {
	value := strings.TrimSpace(string(raw))
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
	}
	if value == "" || value == "null" {
		return "", fmt.Errorf("missing Aster order ID")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return "", fmt.Errorf("invalid Aster order ID")
		}
	}
	return value, nil
}

func parseAsterOrderError(status int, body []byte) string {
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(bytes.TrimSpace(body), &response) == nil && (response.Code != 0 || response.Msg != "") {
		return formatAsterError(response.Code, response.Msg)
	}
	return fmt.Sprintf("Aster order request returned HTTP %d", status)
}

func formatAsterError(code int, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Sprintf("Aster error %d", code)
	}
	return fmt.Sprintf("Aster error %d: %s", code, message)
}
