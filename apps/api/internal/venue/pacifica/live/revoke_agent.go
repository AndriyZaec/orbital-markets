package live

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mr-tron/base58"
)

const (
	revokeAgentURL     = "https://api.pacifica.fi/api/v1/agent/revoke"
	revokeExpiryWindow = 30_000
)

type RevokeAgentRequest struct {
	Account      string `json:"account"`
	Signature    string `json:"signature"`
	Timestamp    int64  `json:"timestamp"`
	ExpiryWindow int64  `json:"expiry_window"`
	AgentWallet  string `json:"agent_wallet"`
}

func (r RevokeAgentRequest) Validate(now time.Time) error {
	owner, err := base58.Decode(r.Account)
	if err != nil || len(owner) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid Pacifica owner account")
	}
	agent, err := base58.Decode(r.AgentWallet)
	if err != nil || len(agent) != ed25519.PublicKeySize || bytes.Equal(owner, agent) {
		return fmt.Errorf("invalid Pacifica agent wallet")
	}
	signature, err := base58.Decode(r.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid Pacifica owner signature")
	}
	if r.ExpiryWindow != revokeExpiryWindow {
		return fmt.Errorf("invalid Pacifica revocation expiry window")
	}
	if delta := now.UnixMilli() - r.Timestamp; delta < -r.ExpiryWindow || delta > r.ExpiryWindow {
		return fmt.Errorf("Pacifica revocation expired; try again")
	}
	message, err := BuildSigningMessage("revoke_agent_wallet", r.Timestamp, r.ExpiryWindow, map[string]any{
		"agent_wallet": r.AgentWallet,
	})
	if err != nil {
		return fmt.Errorf("build Pacifica revocation message: %w", err)
	}
	if !ed25519.Verify(owner, message, signature) {
		return fmt.Errorf("Pacifica owner signature does not revoke this agent")
	}
	return nil
}

type AgentRevoker struct {
	endpoint   string
	httpClient *http.Client
}

func NewAgentRevoker(endpoint string, httpClient *http.Client) *AgentRevoker {
	return &AgentRevoker{endpoint: endpoint, httpClient: httpClient}
}

func NewDefaultAgentRevoker() *AgentRevoker {
	return NewAgentRevoker(revokeAgentURL, &http.Client{Timeout: bindAgentTimeout})
}

func (r *AgentRevoker) RevokeAgent(ctx context.Context, request RevokeAgentRequest) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Pacifica agent revocation: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, bindAgentTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Pacifica agent revocation: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := r.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("relay Pacifica agent revocation: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxBindingResponse))
	if err != nil {
		return fmt.Errorf("read Pacifica agent revocation response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Pacifica rejected agent revocation: %s", pacificaBindingError(responseBody))
	}
	return nil
}
