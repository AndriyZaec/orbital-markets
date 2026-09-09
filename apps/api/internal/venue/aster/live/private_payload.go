package live

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

var privateSymbolPattern = regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)

type PrivateOperation string

const (
	GetPositionMode      PrivateOperation = "get_position_mode"
	GetAccount           PrivateOperation = "get_account"
	GetPositions         PrivateOperation = "get_positions"
	GetLeverageBracket   PrivateOperation = "get_leverage_brackets"
	QueryOrder           PrivateOperation = "query_order"
	UpdateLeverage       PrivateOperation = "update_leverage"
	StartUserStream      PrivateOperation = "start_user_stream"
	KeepaliveUserStream  PrivateOperation = "keepalive_user_stream"
	CloseUserStream      PrivateOperation = "close_user_stream"
	accountRefreshPrefix                  = "aster-account-refresh-"
)

type PrivateRequestParams struct {
	Operation     PrivateOperation
	User          string
	Signer        string
	Symbol        string
	ClientOrderID string
	Leverage      int
}

type AccountSnapshotPayloads struct {
	SnapshotID string                   `json:"snapshot_id"`
	Requests   []*domain.SigningRequest `json:"requests"`
}

type AsterPrivateSubmitMeta struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

func ParsePrivateOperation(value string) (PrivateOperation, bool) {
	operation := PrivateOperation(value)
	switch operation {
	case GetPositionMode, GetAccount, GetPositions, GetLeverageBracket, QueryOrder,
		UpdateLeverage, StartUserStream, KeepaliveUserStream, CloseUserStream:
		return operation, true
	default:
		return "", false
	}
}

func BuildPrivatePayload(params PrivateRequestParams) (*domain.SigningRequest, error) {
	return buildPrivatePayload(params, time.Now(), nextAsterNonce())
}

func buildPrivatePayload(params PrivateRequestParams, now time.Time, nonce int64) (*domain.SigningRequest, error) {
	return buildPrivatePayloadForSnapshot(params, now, nonce, "")
}

func buildPrivatePayloadForSnapshot(
	params PrivateRequestParams,
	now time.Time,
	nonce int64,
	snapshotID string,
) (*domain.SigningRequest, error) {
	if !addressPattern.MatchString(params.User) || !addressPattern.MatchString(params.Signer) {
		return nil, fmt.Errorf("invalid Aster private-request account")
	}
	method, path, requestParams, err := privateOperationSpec(params)
	if err != nil {
		return nil, err
	}
	requestParams = append(requestParams,
		queryParameter{"asterChain", mainnetName},
		queryParameter{"user", params.User},
		queryParameter{"signer", params.Signer},
		queryParameter{"nonce", strconv.FormatInt(nonce, 10)},
	)
	query := encodeQuery(requestParams)
	unsignedBytes, err := json.Marshal(newAsterTypedData(query))
	if err != nil {
		return nil, fmt.Errorf("marshal Aster private request: %w", err)
	}
	metadataBytes, err := json.Marshal(AsterPrivateSubmitMeta{Method: method, Path: path})
	if err != nil {
		return nil, fmt.Errorf("marshal Aster private metadata: %w", err)
	}
	return &domain.SigningRequest{
		ID: fmt.Sprintf("aster-%s-%d", params.Operation, nonce), SnapshotID: snapshotID,
		ClientOrderID: params.ClientOrderID,
		Venue:         "aster", Action: string(params.Operation), Account: params.User, Signer: params.Signer,
		Symbol: params.Symbol, Leverage: params.Leverage,
		UnsignedPayload: unsignedBytes, VenueMetadata: metadataBytes,
		CreatedAt: now, ExpiresAt: now.Add(signingRequestTTL),
	}, nil
}

func BuildAccountSnapshotPayloads(user, signer string) (*AccountSnapshotPayloads, error) {
	return buildAccountPayloads(user, signer, "aster-account-", []PrivateOperation{
		GetPositionMode, GetAccount, GetPositions,
	})
}

func BuildAccountRefreshPayloads(user, signer string) (*AccountSnapshotPayloads, error) {
	return buildAccountPayloads(user, signer, accountRefreshPrefix, []PrivateOperation{GetAccount, GetPositions})
}

func IsAccountRefreshSnapshot(snapshotID string) bool {
	return strings.HasPrefix(snapshotID, accountRefreshPrefix)
}

func buildAccountPayloads(user, signer, prefix string, operations []PrivateOperation) (*AccountSnapshotPayloads, error) {
	now := time.Now()
	snapshotID := fmt.Sprintf("%s%d", prefix, nextAsterNonce())
	requests := make([]*domain.SigningRequest, 0, len(operations))
	for _, operation := range operations {
		request, err := buildPrivatePayloadForSnapshot(PrivateRequestParams{
			Operation: operation, User: user, Signer: signer,
		}, now, nextAsterNonce(), snapshotID)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return &AccountSnapshotPayloads{SnapshotID: snapshotID, Requests: requests}, nil
}

func privateOperationSpec(params PrivateRequestParams) (string, string, []queryParameter, error) {
	switch params.Operation {
	case GetPositionMode:
		return http.MethodGet, "/fapi/v3/positionSide/dual", nil, requireNoPrivateParams(params)
	case GetAccount:
		return http.MethodGet, "/fapi/v3/accountWithJoinMargin", nil, requireNoPrivateParams(params)
	case GetPositions:
		if params.ClientOrderID != "" || params.Leverage != 0 || !validOptionalPrivateSymbol(params.Symbol) {
			return "", "", nil, fmt.Errorf("invalid Aster positions parameters")
		}
		return http.MethodGet, "/fapi/v3/positionRisk", optionalSymbol(params.Symbol), nil
	case GetLeverageBracket:
		if params.ClientOrderID != "" || params.Leverage != 0 || !validOptionalPrivateSymbol(params.Symbol) {
			return "", "", nil, fmt.Errorf("invalid Aster leverage-bracket parameters")
		}
		return http.MethodGet, "/fapi/v3/leverageBracket", optionalSymbol(params.Symbol), nil
	case QueryOrder:
		if !privateSymbolPattern.MatchString(params.Symbol) || !clientIDPattern.MatchString(params.ClientOrderID) || params.Leverage != 0 {
			return "", "", nil, fmt.Errorf("invalid Aster order-query parameters")
		}
		return http.MethodGet, "/fapi/v3/order", []queryParameter{
			{"symbol", params.Symbol}, {"origClientOrderId", params.ClientOrderID},
		}, nil
	case UpdateLeverage:
		if !privateSymbolPattern.MatchString(params.Symbol) || params.ClientOrderID != "" || params.Leverage < 1 || params.Leverage > 125 {
			return "", "", nil, fmt.Errorf("invalid Aster leverage-update parameters")
		}
		return http.MethodPost, "/fapi/v3/leverage", []queryParameter{
			{"symbol", params.Symbol}, {"leverage", strconv.Itoa(params.Leverage)},
		}, nil
	case StartUserStream:
		return http.MethodPost, "/fapi/v3/listenKey", nil, requireNoPrivateParams(params)
	case KeepaliveUserStream:
		return http.MethodPut, "/fapi/v3/listenKey", nil, requireNoPrivateParams(params)
	case CloseUserStream:
		return http.MethodDelete, "/fapi/v3/listenKey", nil, requireNoPrivateParams(params)
	default:
		return "", "", nil, fmt.Errorf("unsupported Aster private operation: %s", params.Operation)
	}
}

func requireNoPrivateParams(params PrivateRequestParams) error {
	if params.Symbol != "" || params.ClientOrderID != "" || params.Leverage != 0 {
		return fmt.Errorf("unexpected Aster private-request parameters")
	}
	return nil
}

func optionalSymbol(symbol string) []queryParameter {
	if symbol == "" {
		return nil
	}
	return []queryParameter{{"symbol", symbol}}
}

func validOptionalPrivateSymbol(symbol string) bool {
	return symbol == "" || privateSymbolPattern.MatchString(symbol)
}

func newAsterTypedData(query string) AsterUnsignedOrder {
	return AsterUnsignedOrder{
		Domain: EIP712Domain{
			Name: "AsterSignTransaction", Version: "1", ChainID: mainnetChainID,
			VerifyingContract: "0x0000000000000000000000000000000000000000",
		},
		Types: EIP712Types{
			Domain: []EIP712Field{
				{Name: "name", Type: "string"}, {Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"},
			},
			Message: []EIP712Field{{Name: "msg", Type: "string"}},
		},
		PrimaryType: "Message", Message: AsterMessage{Msg: query},
	}
}
