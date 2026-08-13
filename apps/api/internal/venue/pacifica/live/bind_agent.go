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
	bindAgentURL       = "https://api.pacifica.fi/api/v1/agent/bind"
	bindAgentTimeout   = 10 * time.Second
	bindExpiryWindow   = 30_000
	maxBindingResponse = 64 << 10
)

type BindAgentRequest struct {
	Account      string `json:"account"`
	Signature    string `json:"signature"`
	Timestamp    int64  `json:"timestamp"`
	ExpiryWindow int64  `json:"expiry_window"`
	AgentWallet  string `json:"agent_wallet"`
}

func (r BindAgentRequest) Validate(now time.Time) error {
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
	if r.ExpiryWindow != bindExpiryWindow {
		return fmt.Errorf("invalid Pacifica binding expiry window")
	}
	if delta := now.UnixMilli() - r.Timestamp; delta < -r.ExpiryWindow || delta > r.ExpiryWindow {
		return fmt.Errorf("Pacifica authorization expired; try again")
	}
	message, err := BuildSigningMessage("bind_agent_wallet", r.Timestamp, r.ExpiryWindow, map[string]any{
		"agent_wallet": r.AgentWallet,
	})
	if err != nil {
		return fmt.Errorf("build Pacifica binding message: %w", err)
	}
	if !ed25519.Verify(owner, message, signature) {
		return fmt.Errorf("Pacifica owner signature does not authorize this agent")
	}
	return nil
}

type AgentBinder struct {
	endpoint   string
	httpClient *http.Client
}

func NewAgentBinder(endpoint string, httpClient *http.Client) *AgentBinder {
	return &AgentBinder{endpoint: endpoint, httpClient: httpClient}
}

func NewDefaultAgentBinder() *AgentBinder {
	return NewAgentBinder(bindAgentURL, &http.Client{Timeout: bindAgentTimeout})
}

func (b *AgentBinder) BindAgent(ctx context.Context, request BindAgentRequest) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Pacifica agent binding: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, bindAgentTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, b.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Pacifica agent binding: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := b.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("relay Pacifica agent binding: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxBindingResponse))
	if err != nil {
		return fmt.Errorf("read Pacifica agent binding response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Pacifica rejected agent binding: %s", pacificaBindingError(responseBody))
	}
	return nil
}

func pacificaBindingError(body []byte) string {
	var response struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &response) == nil {
		if response.Error != "" {
			return response.Error
		}
		if response.Message != "" {
			return response.Message
		}
	}
	return "venue rejected request"
}
