package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	agentApprovalURL      = "https://fapi.asterdex.com/fapi/v3/approveAgent"
	builderApprovalURL    = "https://fapi.asterdex.com/fapi/v3/approveBuilder"
	agentApprovalTimeout  = 10 * time.Second
	maxApprovalResponse   = 64 << 10
	maxApprovalAge        = 60 * time.Second
	maxApprovalFutureSkew = 5 * time.Second
	maxAgentLifetime      = 30 * 24 * time.Hour
	asterSignatureChainID = 56
	asterAgentName        = "OrbitalMarkets"
)

var ethereumSignaturePattern = regexp.MustCompile(`^0x[0-9a-fA-F]{130}$`)

var ErrApprovalRejected = errors.New("Aster approval was rejected")

type ApproveAgentRequest struct {
	User             string `json:"user"`
	Nonce            int64  `json:"nonce"`
	Signature        string `json:"signature"`
	AgentName        string `json:"agentName"`
	AgentAddress     string `json:"agentAddress"`
	IPWhitelist      string `json:"ipWhitelist"`
	Expired          int64  `json:"expired"`
	CanSpotTrade     bool   `json:"canSpotTrade"`
	CanPerpTrade     bool   `json:"canPerpTrade"`
	CanWithdraw      bool   `json:"canWithdraw"`
	AsterChain       string `json:"asterChain"`
	SignatureChainID int    `json:"signatureChainId"`
	Builder          string `json:"builder"`
	MaxFeeRate       string `json:"maxFeeRate"`
	BuilderName      string `json:"builderName"`
	BuilderNonce     int64  `json:"builderNonce"`
	BuilderSignature string `json:"builderSignature"`
}

func (r ApproveAgentRequest) Validate(now time.Time, builder BuilderConfig) error {
	if !addressPattern.MatchString(r.User) || !addressPattern.MatchString(r.AgentAddress) ||
		strings.EqualFold(r.User, r.AgentAddress) {
		return fmt.Errorf("invalid Aster owner or agent address")
	}
	if r.AgentName != asterAgentName || r.IPWhitelist != "" || r.AsterChain != mainnetName ||
		r.SignatureChainID != asterSignatureChainID {
		return fmt.Errorf("invalid Aster agent approval")
	}
	if r.CanSpotTrade || !r.CanPerpTrade || r.CanWithdraw {
		return fmt.Errorf("Aster agent approval must be perpetual-only without withdrawals")
	}
	if !strings.EqualFold(r.Builder, builder.Address) || r.MaxFeeRate != builder.FeeRate || r.BuilderName != asterAgentName {
		return fmt.Errorf("invalid Aster builder approval")
	}
	nonceTime := time.UnixMicro(r.Nonce)
	if nonceTime.Before(now.Add(-maxApprovalAge)) || nonceTime.After(now.Add(maxApprovalFutureSkew)) {
		return fmt.Errorf("Aster agent approval nonce is stale")
	}
	expiresAt := time.UnixMilli(r.Expired)
	if !expiresAt.After(now) || expiresAt.After(now.Add(maxAgentLifetime)) {
		return fmt.Errorf("invalid Aster agent approval expiry")
	}
	if err := validateEthereumSignature(r.Signature); err != nil {
		return fmt.Errorf("invalid Aster owner signature: %w", err)
	}
	builderNonceTime := time.UnixMicro(r.BuilderNonce)
	if builderNonceTime.Before(now.Add(-maxApprovalAge)) || builderNonceTime.After(now.Add(maxApprovalFutureSkew)) {
		return fmt.Errorf("Aster builder approval nonce is stale")
	}
	if err := validateEthereumSignature(r.BuilderSignature); err != nil {
		return fmt.Errorf("invalid Aster builder signature: %w", err)
	}
	return nil
}

func validateEthereumSignature(signature string) error {
	if !ethereumSignaturePattern.MatchString(signature) {
		return fmt.Errorf("invalid Ethereum signature")
	}
	recovery, err := strconv.ParseUint(signature[len(signature)-2:], 16, 8)
	if err != nil || (recovery != 0 && recovery != 1 && recovery != 27 && recovery != 28) {
		return fmt.Errorf("invalid Ethereum signature recovery value")
	}
	return nil
}

type AgentApprover struct {
	agentEndpoint   string
	builderEndpoint string
	httpClient      *http.Client
}

func NewAgentApprover(agentEndpoint, builderEndpoint string, httpClient *http.Client) *AgentApprover {
	return &AgentApprover{agentEndpoint: agentEndpoint, builderEndpoint: builderEndpoint, httpClient: httpClient}
}

func NewDefaultAgentApprover() *AgentApprover {
	return NewAgentApprover(agentApprovalURL, builderApprovalURL, &http.Client{Timeout: agentApprovalTimeout})
}

func (a *AgentApprover) ApproveAgent(ctx context.Context, request ApproveAgentRequest) error {
	builderQuery := encodeQuery([]queryParameter{
		{"builder", request.Builder},
		{"maxFeeRate", request.MaxFeeRate},
		{"builderName", request.BuilderName},
		{"asterChain", request.AsterChain},
		{"user", request.User},
		{"nonce", strconv.FormatInt(request.BuilderNonce, 10)},
		{"signatureChainId", strconv.Itoa(request.SignatureChainID)},
		{"signature", request.BuilderSignature},
	})
	if err := a.postApproval(ctx, a.builderEndpoint, builderQuery, "builder"); err != nil {
		if errors.Is(err, ErrSubmissionNotSent) || errors.Is(err, ErrSubmissionAmbiguous) {
			return fmt.Errorf("%w: Aster builder approval did not complete: %v", ErrSubmissionNotSent, err)
		}
		return err
	}

	agentQuery := encodeQuery([]queryParameter{
		{"agentName", request.AgentName},
		{"agentAddress", request.AgentAddress},
		{"ipWhitelist", request.IPWhitelist},
		{"expired", strconv.FormatInt(request.Expired, 10)},
		{"canSpotTrade", strconv.FormatBool(request.CanSpotTrade)},
		{"canPerpTrade", strconv.FormatBool(request.CanPerpTrade)},
		{"canWithdraw", strconv.FormatBool(request.CanWithdraw)},
		{"asterChain", request.AsterChain},
		{"user", request.User},
		{"nonce", strconv.FormatInt(request.Nonce, 10)},
		{"signatureChainId", strconv.Itoa(request.SignatureChainID)},
		{"signature", request.Signature},
	})
	return a.postApproval(ctx, a.agentEndpoint, agentQuery, "agent")
}

func (a *AgentApprover) postApproval(ctx context.Context, endpoint, body, kind string) error {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build Aster %s approval: %v", ErrSubmissionNotSent, kind, err)
	}
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("%w: relay Aster %s approval: %v", ErrSubmissionAmbiguous, kind, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxApprovalResponse+1))
	if err != nil {
		return fmt.Errorf("%w: read Aster %s approval response: %v", ErrSubmissionAmbiguous, kind, err)
	}
	if len(responseBody) > maxApprovalResponse {
		return fmt.Errorf("%w: Aster %s approval response exceeds %d bytes", ErrSubmissionAmbiguous, kind, maxApprovalResponse)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: Aster %s approval returned HTTP %d", ErrSubmissionAmbiguous, kind, response.StatusCode)
		}
		return fmt.Errorf("%w: Aster %s approval returned HTTP %d: %s", ErrApprovalRejected, kind, response.StatusCode, string(responseBody))
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return fmt.Errorf("%w: decode Aster %s approval response", ErrSubmissionAmbiguous, kind)
	}
	if result.Code != http.StatusOK {
		return fmt.Errorf("%w: Aster %s approval: %s", ErrApprovalRejected, kind, result.Msg)
	}
	return nil
}
