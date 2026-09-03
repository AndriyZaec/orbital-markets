package live

import (
	"bytes"
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/mr-tron/base58"
)

const (
	approveBuilderCodeURL      = "https://api.pacifica.fi/api/v1/account/builder_codes/approve"
	builderApprovalTimeout     = 10 * time.Second
	builderApprovalExpiry      = 30_000
	maxBuilderApprovalResponse = 64 << 10
)

//go:embed builder_config.json
var builderConfigJSON []byte

var OrbitalBuilder = mustLoadBuilderConfig()

type BuilderConfig struct {
	Code        string `json:"code"`
	Owner       string `json:"owner"`
	Description string `json:"description"`
	FeeRate     string `json:"feeRate"`
	MaxFeeRate  string `json:"maxFeeRate"`
}

func mustLoadBuilderConfig() BuilderConfig {
	var config BuilderConfig
	if err := json.Unmarshal(builderConfigJSON, &config); err != nil {
		panic("decode Pacifica builder config: " + err.Error())
	}
	owner, ownerErr := base58.Decode(config.Owner)
	feeRate, feeErr := strconv.ParseFloat(config.FeeRate, 64)
	maxFeeRate, maxFeeErr := strconv.ParseFloat(config.MaxFeeRate, 64)
	if !regexp.MustCompile(`^[A-Za-z0-9]{3,16}$`).MatchString(config.Code) ||
		ownerErr != nil || len(owner) != ed25519.PublicKeySize ||
		config.Description == "" ||
		feeErr != nil || maxFeeErr != nil || feeRate <= 0 || maxFeeRate < feeRate {
		panic("invalid Pacifica builder config")
	}
	return config
}

func OrbitalBuilderConfig() *BuilderConfig {
	config := OrbitalBuilder
	return &config
}

type ApproveBuilderCodeRequest struct {
	Account      string  `json:"account"`
	AgentWallet  *string `json:"agent_wallet"`
	Signature    string  `json:"signature"`
	Timestamp    int64   `json:"timestamp"`
	ExpiryWindow int64   `json:"expiry_window"`
	BuilderCode  string  `json:"builder_code"`
	MaxFeeRate   string  `json:"max_fee_rate"`
}

func (r ApproveBuilderCodeRequest) Validate(now time.Time, expected BuilderConfig) error {
	owner, err := base58.Decode(r.Account)
	if err != nil || len(owner) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid Pacifica owner account")
	}
	if r.AgentWallet != nil {
		return fmt.Errorf("Pacifica builder approval must be signed by the owner")
	}
	signature, err := base58.Decode(r.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid Pacifica owner signature")
	}
	if r.BuilderCode != expected.Code || r.MaxFeeRate != expected.MaxFeeRate {
		return fmt.Errorf("invalid Pacifica builder approval")
	}
	if r.ExpiryWindow != builderApprovalExpiry {
		return fmt.Errorf("invalid Pacifica builder approval expiry window")
	}
	if delta := now.UnixMilli() - r.Timestamp; delta < -r.ExpiryWindow || delta > r.ExpiryWindow {
		return fmt.Errorf("Pacifica builder approval expired; try again")
	}
	message, err := BuildSigningMessage("approve_builder_code", r.Timestamp, r.ExpiryWindow, map[string]any{
		"builder_code": r.BuilderCode,
		"max_fee_rate": r.MaxFeeRate,
	})
	if err != nil {
		return fmt.Errorf("build Pacifica builder approval message: %w", err)
	}
	if !ed25519.Verify(owner, message, signature) {
		return fmt.Errorf("Pacifica owner signature does not approve this builder code")
	}
	return nil
}

type BuilderCodeApprover struct {
	endpoint   string
	httpClient *http.Client
}

func NewBuilderCodeApprover(endpoint string, httpClient *http.Client) *BuilderCodeApprover {
	return &BuilderCodeApprover{endpoint: endpoint, httpClient: httpClient}
}

func NewDefaultBuilderCodeApprover() *BuilderCodeApprover {
	return NewBuilderCodeApprover(approveBuilderCodeURL, &http.Client{Timeout: builderApprovalTimeout})
}

func (a *BuilderCodeApprover) ApproveBuilderCode(ctx context.Context, request ApproveBuilderCodeRequest) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Pacifica builder approval: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, builderApprovalTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Pacifica builder approval: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := a.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("relay Pacifica builder approval: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxBuilderApprovalResponse))
	if readErr != nil {
		return fmt.Errorf("read Pacifica builder approval response: %w", readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Pacifica builder approval returned HTTP %d: %s", response.StatusCode, string(responseBody))
	}
	return nil
}
