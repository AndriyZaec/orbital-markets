package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	agentApprovalURL      = "https://fapi.asterdex.com/fapi/v3/approveAgent"
	agentApprovalTimeout  = 10 * time.Second
	maxApprovalResponse   = 64 << 10
	maxApprovalAge        = 60 * time.Second
	maxApprovalFutureSkew = 5 * time.Second
	maxAgentLifetime      = 30 * 24 * time.Hour
	asterSignatureChainID = 56
	asterAgentName        = "Orbital Markets"
)

var ethereumSignaturePattern = regexp.MustCompile(`^0x[0-9a-fA-F]{130}$`)

type ApproveAgentRequest struct {
	User             string `json:"user"`
	Nonce            int64  `json:"nonce"`
	Signature        string `json:"signature"`
	AgentName        string `json:"agentName"`
	AgentAddress     string `json:"agentAddress"`
	Expired          int64  `json:"expired"`
	CanSpotTrade     bool   `json:"canSpotTrade"`
	CanPerpTrade     bool   `json:"canPerpTrade"`
	CanWithdraw      bool   `json:"canWithdraw"`
	Builder          string `json:"builder"`
	MaxFeeRate       string `json:"maxFeeRate"`
	BuilderName      string `json:"builderName"`
	AsterChain       string `json:"asterChain"`
	SignatureChainID int    `json:"signatureChainId"`
}

func (r ApproveAgentRequest) Validate(now time.Time, builder BuilderConfig) error {
	if !addressPattern.MatchString(r.User) || !addressPattern.MatchString(r.AgentAddress) ||
		strings.EqualFold(r.User, r.AgentAddress) {
		return fmt.Errorf("invalid Aster owner or agent address")
	}
	if r.AgentName != asterAgentName || r.AsterChain != mainnetName ||
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
	endpoint   string
	httpClient *http.Client
}

func NewAgentApprover(endpoint string, httpClient *http.Client) *AgentApprover {
	return &AgentApprover{endpoint: endpoint, httpClient: httpClient}
}

func NewDefaultAgentApprover() *AgentApprover {
	return NewAgentApprover(agentApprovalURL, &http.Client{Timeout: agentApprovalTimeout})
}

func (a *AgentApprover) ApproveAgent(ctx context.Context, request ApproveAgentRequest) error {
	values := url.Values{
		"user":             {request.User},
		"nonce":            {strconv.FormatInt(request.Nonce, 10)},
		"signature":        {request.Signature},
		"agentName":        {request.AgentName},
		"agentAddress":     {request.AgentAddress},
		"expired":          {strconv.FormatInt(request.Expired, 10)},
		"canSpotTrade":     {strconv.FormatBool(request.CanSpotTrade)},
		"canPerpTrade":     {strconv.FormatBool(request.CanPerpTrade)},
		"canWithdraw":      {strconv.FormatBool(request.CanWithdraw)},
		"builder":          {request.Builder},
		"maxFeeRate":       {request.MaxFeeRate},
		"builderName":      {request.BuilderName},
		"asterChain":       {request.AsterChain},
		"signatureChainId": {strconv.Itoa(request.SignatureChainID)},
	}
	body := values.Encode()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Aster agent approval: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("relay Aster agent approval: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxApprovalResponse))
	if err != nil {
		return fmt.Errorf("read Aster agent approval response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Aster agent approval returned HTTP %d: %s", response.StatusCode, string(responseBody))
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil || result.Code != http.StatusOK {
		return fmt.Errorf("Aster agent approval rejected: %s", string(responseBody))
	}
	return nil
}
