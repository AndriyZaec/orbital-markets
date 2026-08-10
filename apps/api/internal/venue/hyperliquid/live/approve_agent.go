package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	agentApprovalTimeout = 10 * time.Second
	maxApprovalResponse  = 64 << 10
)

var (
	evmAddressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	hexScalarPattern  = regexp.MustCompile(`^0x[0-9a-fA-F]{1,64}$`)
	hexChainPattern   = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
)

type ApproveAgentAction struct {
	Type             string `json:"type"`
	HyperliquidChain string `json:"hyperliquidChain"`
	SignatureChainID string `json:"signatureChainId"`
	AgentAddress     string `json:"agentAddress"`
	AgentName        string `json:"agentName"`
	Nonce            int64  `json:"nonce"`
}

type EthereumSignature struct {
	R string `json:"r"`
	S string `json:"s"`
	V int    `json:"v"`
}

type ApproveAgentRequest struct {
	Action    ApproveAgentAction `json:"action"`
	Signature EthereumSignature  `json:"signature"`
}

func (r ApproveAgentRequest) Validate(now time.Time) error {
	if r.Action.Type != "approveAgent" || r.Action.HyperliquidChain != "Mainnet" ||
		r.Action.AgentName != "Orbital Markets" {
		return fmt.Errorf("invalid Hyperliquid agent approval action")
	}
	if !evmAddressPattern.MatchString(r.Action.AgentAddress) {
		return fmt.Errorf("invalid Hyperliquid agent address")
	}
	if !hexChainPattern.MatchString(r.Action.SignatureChainID) {
		return fmt.Errorf("invalid Hyperliquid signature chain ID")
	}
	chainID, err := strconv.ParseUint(strings.TrimPrefix(r.Action.SignatureChainID, "0x"), 16, 64)
	if err != nil || chainID == 0 {
		return fmt.Errorf("invalid Hyperliquid signature chain ID")
	}
	if delta := now.UnixMilli() - r.Action.Nonce; delta < -30_000 || delta > 30_000 {
		return fmt.Errorf("Hyperliquid agent approval nonce is stale")
	}
	if !hexScalarPattern.MatchString(r.Signature.R) || !hexScalarPattern.MatchString(r.Signature.S) ||
		(r.Signature.V != 27 && r.Signature.V != 28) {
		return fmt.Errorf("invalid Hyperliquid agent approval signature")
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
	return NewAgentApprover(exchangeURL, &http.Client{Timeout: agentApprovalTimeout})
}

func (a *AgentApprover) ApproveAgent(ctx context.Context, request ApproveAgentRequest) error {
	body, err := json.Marshal(struct {
		Action       ApproveAgentAction `json:"action"`
		Nonce        int64              `json:"nonce"`
		Signature    EthereumSignature  `json:"signature"`
		VaultAddress *string            `json:"vaultAddress"`
		ExpiresAfter *int64             `json:"expiresAfter"`
	}{
		Action: request.Action, Nonce: request.Action.Nonce, Signature: request.Signature,
	})
	if err != nil {
		return fmt.Errorf("encode Hyperliquid agent approval: %w", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, agentApprovalTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Hyperliquid agent approval: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := a.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("relay Hyperliquid agent approval: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxApprovalResponse))
	if err != nil {
		return fmt.Errorf("read Hyperliquid agent approval response: %w", err)
	}

	var result struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return fmt.Errorf("invalid Hyperliquid agent approval response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Status != "ok" {
		return fmt.Errorf("Hyperliquid rejected agent approval: %s", venueApprovalError(result.Response))
	}
	return nil
}

func venueApprovalError(response json.RawMessage) string {
	var message string
	if json.Unmarshal(response, &message) == nil && message != "" {
		return message
	}
	return "venue rejected request"
}
