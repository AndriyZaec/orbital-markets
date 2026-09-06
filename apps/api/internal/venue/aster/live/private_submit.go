package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
)

const maxPrivateResponse = 2 << 20

type PrivateResult struct {
	Operation     PrivateOperation     `json:"operation"`
	Data          json.RawMessage      `json:"data"`
	AccountUpdate *asteraccount.Update `json:"-"`
	SubmittedAt   time.Time            `json:"submitted_at"`
	RespondedAt   time.Time            `json:"responded_at"`
}

func (c *Client) SubmitSignedPrivate(
	ctx context.Context,
	signed domain.SignedAction,
	request *domain.SigningRequest,
) (*PrivateResult, error) {
	operation, metadata, query, err := validateSignedPrivate(signed, request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSubmissionNotSent, err)
	}
	signedQuery := query + "&signature=" + url.QueryEscape(signed.Signature)
	endpoint := c.baseURL + metadata.Path
	var body io.Reader
	if metadata.Method == http.MethodGet {
		endpoint += "?" + signedQuery
	} else {
		body = strings.NewReader(signedQuery)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, metadata.Method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("%w: build private request: %v", ErrSubmissionNotSent, err)
	}
	if body != nil {
		httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	submittedAt := time.Now()
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("%w: submit Aster %s: %v", ErrSubmissionAmbiguous, operation, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxPrivateResponse+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read Aster %s response: %v", ErrSubmissionAmbiguous, operation, err)
	}
	if len(responseBody) > maxPrivateResponse {
		return nil, fmt.Errorf("%w: Aster %s response exceeds %d bytes", ErrSubmissionAmbiguous, operation, maxPrivateResponse)
	}
	if response.StatusCode >= http.StatusInternalServerError {
		return nil, fmt.Errorf("%w: Aster %s returned HTTP %d", ErrSubmissionAmbiguous, operation, response.StatusCode)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("submit Aster %s: %s", operation, parseAsterOrderError(response.StatusCode, responseBody))
	}
	if !json.Valid(responseBody) {
		return nil, fmt.Errorf("%w: decode Aster %s response", ErrSubmissionAmbiguous, operation)
	}
	var venueError struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(responseBody, &venueError) == nil && venueError.Code != 0 && venueError.Code != http.StatusOK {
		return nil, fmt.Errorf("submit Aster %s: %s", operation, formatAsterError(venueError.Code, venueError.Msg))
	}
	accountUpdate, err := validatePrivateResponse(operation, request, responseBody)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid Aster %s response: %v", ErrSubmissionAmbiguous, operation, err)
	}
	return &PrivateResult{
		Operation: operation, Data: append(json.RawMessage(nil), responseBody...), AccountUpdate: accountUpdate,
		SubmittedAt: submittedAt, RespondedAt: time.Now(),
	}, nil
}

func validatePrivateResponse(operation PrivateOperation, request *domain.SigningRequest, body []byte) (*asteraccount.Update, error) {
	switch operation {
	case GetPositionMode, GetAccount, GetPositions:
		part, err := asteraccount.ParseSnapshotPart(string(operation), body)
		if err != nil {
			return nil, err
		}
		if operation == GetPositions && request.Symbol != "" {
			for _, position := range *part.Positions {
				if position.Symbol != request.Symbol {
					return nil, fmt.Errorf("position correlation mismatch")
				}
			}
		}
		return &asteraccount.Update{SnapshotPart: &part}, nil
	case GetLeverageBracket:
		brackets, err := asteraccount.ParseLeverageBrackets(body, request.Symbol)
		if err != nil {
			return nil, err
		}
		return &asteraccount.Update{LeverageBrackets: brackets}, nil
	case QueryOrder:
		var response struct {
			OrderID       json.RawMessage `json:"orderId"`
			ClientOrderID string          `json:"clientOrderId"`
			Symbol        string          `json:"symbol"`
			Status        string          `json:"status"`
			ExecutedQty   string          `json:"executedQty"`
			AvgPrice      string          `json:"avgPrice"`
		}
		if json.Unmarshal(body, &response) != nil || response.ClientOrderID != request.ClientOrderID ||
			response.Symbol != request.Symbol || !validOrderStatus(response.Status) ||
			!validFiniteDecimal(response.ExecutedQty) || !validFiniteDecimal(response.AvgPrice) {
			return nil, fmt.Errorf("order correlation mismatch")
		}
		if _, err := parseAsterOrderID(response.OrderID); err != nil {
			return nil, err
		}
	case UpdateLeverage:
		update, err := asteraccount.ParseLeverageUpdate(body)
		if err != nil || update.Symbol != request.Symbol || update.Leverage != float64(request.Leverage) {
			return nil, fmt.Errorf("leverage correlation mismatch")
		}
		return &asteraccount.Update{Leverage: &update}, nil
	case StartUserStream:
		var response struct {
			ListenKey string `json:"listenKey"`
		}
		if json.Unmarshal(body, &response) != nil || len(response.ListenKey) < 16 || len(response.ListenKey) > 256 ||
			strings.ContainsAny(response.ListenKey, "/?# \\") {
			return nil, fmt.Errorf("invalid listen key")
		}
	case KeepaliveUserStream, CloseUserStream:
		var response map[string]json.RawMessage
		if json.Unmarshal(body, &response) != nil || response == nil {
			return nil, fmt.Errorf("invalid stream response")
		}
	default:
		return nil, fmt.Errorf("unsupported private operation")
	}
	return nil, nil
}

func validFiniteDecimal(value string) bool {
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func validOrderStatus(status string) bool {
	switch status {
	case "NEW", "PARTIALLY_FILLED", "FILLED", "CANCELED", "REJECTED", "EXPIRED":
		return true
	default:
		return false
	}
}

func validateSignedPrivate(
	signed domain.SignedAction,
	request *domain.SigningRequest,
) (PrivateOperation, AsterPrivateSubmitMeta, string, error) {
	if request == nil || request.Venue != "aster" || signed.Venue != "aster" {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("invalid Aster private signing context")
	}
	if time.Now().After(request.ExpiresAt) {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("Aster private signing request expired")
	}
	if signed.RequestID != request.ID || signed.ClientOrderID != request.ClientOrderID ||
		!strings.EqualFold(signed.SignerAddress, request.Signer) {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("Aster private signed action correlation mismatch")
	}
	if err := validateEthereumSignature(signed.Signature); err != nil {
		return "", AsterPrivateSubmitMeta{}, "", err
	}
	operation := PrivateOperation(request.Action)
	params := PrivateRequestParams{
		Operation: operation, User: request.Account, Signer: request.Signer,
		Symbol: request.Symbol, ClientOrderID: request.ClientOrderID, Leverage: request.Leverage,
	}
	method, path, operationParams, err := privateOperationSpec(params)
	if err != nil || request.Side != "" || request.Amount != 0 || request.Price != 0 || request.ReduceOnly {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("invalid Aster private request summary")
	}
	var metadata AsterPrivateSubmitMeta
	if err := json.Unmarshal(request.VenueMetadata, &metadata); err != nil || metadata.Method != method || metadata.Path != path {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("invalid Aster private request metadata")
	}
	var unsigned AsterUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil || !validAsterTypedData(unsigned) {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("invalid Aster private typed data")
	}
	values, err := url.ParseQuery(unsigned.Message.Msg)
	if err != nil || len(values["nonce"]) != 1 {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("invalid Aster private nonce")
	}
	nonce, err := strconv.ParseInt(values.Get("nonce"), 10, 64)
	if err != nil || nonce < request.CreatedAt.UnixMicro() || nonce > request.ExpiresAt.UnixMicro() {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("invalid Aster private nonce")
	}
	expectedParams := append(operationParams,
		queryParameter{"asterChain", mainnetName}, queryParameter{"user", request.Account},
		queryParameter{"signer", request.Signer}, queryParameter{"nonce", strconv.FormatInt(nonce, 10)},
	)
	if unsigned.Message.Msg != encodeQuery(expectedParams) {
		return "", AsterPrivateSubmitMeta{}, "", fmt.Errorf("invalid Aster private signed query")
	}
	return operation, metadata, unsigned.Message.Msg, nil
}

func validAsterTypedData(unsigned AsterUnsignedOrder) bool {
	return unsigned.Domain.Name == "AsterSignTransaction" && unsigned.Domain.Version == "1" &&
		unsigned.Domain.ChainID == mainnetChainID &&
		strings.EqualFold(unsigned.Domain.VerifyingContract, "0x0000000000000000000000000000000000000000") &&
		unsigned.PrimaryType == "Message" && unsigned.Message.Msg != "" &&
		equalEIP712Fields(unsigned.Types.Domain, []EIP712Field{
			{Name: "name", Type: "string"}, {Name: "version", Type: "string"},
			{Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"},
		}) && equalEIP712Fields(unsigned.Types.Message, []EIP712Field{{Name: "msg", Type: "string"}})
}
