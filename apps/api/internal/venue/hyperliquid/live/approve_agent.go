package live

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

const (
	agentApprovalTimeout  = 10 * time.Second
	maxApprovalResponse   = 64 << 10
	maxApprovalAge        = 5 * time.Minute
	maxApprovalFutureSkew = 30 * time.Second
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
	OwnerAddress string             `json:"owner_address"`
	Action       ApproveAgentAction `json:"action"`
	Signature    EthereumSignature  `json:"signature"`
}

type ApproveBuilderFeeAction struct {
	Type             string `json:"type"`
	HyperliquidChain string `json:"hyperliquidChain"`
	SignatureChainID string `json:"signatureChainId"`
	MaxFeeRate       string `json:"maxFeeRate"`
	Builder          string `json:"builder"`
	Nonce            int64  `json:"nonce"`
}

type ApproveBuilderFeeRequest struct {
	OwnerAddress string                  `json:"owner_address"`
	Action       ApproveBuilderFeeAction `json:"action"`
	Signature    EthereumSignature       `json:"signature"`
}

func (r ApproveBuilderFeeRequest) Validate(now time.Time, expectedBuilder string) error {
	if r.Action.Type != "approveBuilderFee" || r.Action.HyperliquidChain != "Mainnet" ||
		r.Action.MaxFeeRate != OrbitalBuilder.MaxFeeRate || !strings.EqualFold(r.Action.Builder, expectedBuilder) {
		return fmt.Errorf("invalid Hyperliquid builder fee approval action")
	}
	if !evmAddressPattern.MatchString(r.Action.Builder) || !evmAddressPattern.MatchString(r.OwnerAddress) ||
		!hexChainPattern.MatchString(r.Action.SignatureChainID) {
		return fmt.Errorf("invalid Hyperliquid builder fee approval action")
	}
	if delta := time.Duration(now.UnixMilli()-r.Action.Nonce) * time.Millisecond; delta < -maxApprovalFutureSkew || delta > maxApprovalAge {
		return fmt.Errorf("Hyperliquid builder fee approval nonce is stale")
	}
	if !hexScalarPattern.MatchString(r.Signature.R) || !hexScalarPattern.MatchString(r.Signature.S) ||
		(r.Signature.V != 27 && r.Signature.V != 28) {
		return fmt.Errorf("invalid Hyperliquid builder fee approval signature")
	}
	digest, err := r.SigningHash()
	if err != nil {
		return err
	}
	return verifyEthereumOwner(r.Signature, digest, r.OwnerAddress)
}

func (r ApproveBuilderFeeRequest) SigningHash() ([32]byte, error) {
	chainID, err := strconv.ParseUint(strings.TrimPrefix(r.Action.SignatureChainID, "0x"), 16, 64)
	if err != nil || chainID == 0 {
		return [32]byte{}, fmt.Errorf("invalid Hyperliquid signature chain ID")
	}
	builder, err := addressWord(r.Action.Builder)
	if err != nil {
		return [32]byte{}, fmt.Errorf("invalid Hyperliquid builder address")
	}
	domain := keccak256(
		keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")),
		keccak256([]byte("HyperliquidSignTransaction")), keccak256([]byte("1")), uintWord(chainID), make([]byte, 32),
	)
	message := keccak256(
		keccak256([]byte("HyperliquidTransaction:ApproveBuilderFee(string hyperliquidChain,string maxFeeRate,address builder,uint64 nonce)")),
		keccak256([]byte(r.Action.HyperliquidChain)), keccak256([]byte(r.Action.MaxFeeRate)), builder, uintWord(uint64(r.Action.Nonce)),
	)
	digest := keccak256([]byte{0x19, 0x01}, domain, message)
	var result [32]byte
	copy(result[:], digest)
	return result, nil
}

func (r ApproveAgentRequest) Validate(now time.Time) error {
	if r.Action.Type != "approveAgent" || r.Action.HyperliquidChain != "Mainnet" ||
		r.Action.AgentName != "Orbital Markets" {
		return fmt.Errorf("invalid Hyperliquid agent approval action")
	}
	if !evmAddressPattern.MatchString(r.Action.AgentAddress) {
		return fmt.Errorf("invalid Hyperliquid agent address")
	}
	if !evmAddressPattern.MatchString(r.OwnerAddress) {
		return fmt.Errorf("invalid Hyperliquid owner address")
	}
	if !hexChainPattern.MatchString(r.Action.SignatureChainID) {
		return fmt.Errorf("invalid Hyperliquid signature chain ID")
	}
	chainID, err := strconv.ParseUint(strings.TrimPrefix(r.Action.SignatureChainID, "0x"), 16, 64)
	if err != nil || chainID == 0 {
		return fmt.Errorf("invalid Hyperliquid signature chain ID")
	}
	if delta := time.Duration(now.UnixMilli()-r.Action.Nonce) * time.Millisecond; delta < -maxApprovalFutureSkew || delta > maxApprovalAge {
		return fmt.Errorf("Hyperliquid agent approval nonce is stale")
	}
	if !hexScalarPattern.MatchString(r.Signature.R) || !hexScalarPattern.MatchString(r.Signature.S) ||
		(r.Signature.V != 27 && r.Signature.V != 28) {
		return fmt.Errorf("invalid Hyperliquid agent approval signature")
	}
	if err := r.verifyOwnerSignature(); err != nil {
		return err
	}
	return nil
}

func (r ApproveAgentRequest) SigningHash() ([32]byte, error) {
	chainID, err := strconv.ParseUint(strings.TrimPrefix(r.Action.SignatureChainID, "0x"), 16, 64)
	if err != nil || chainID == 0 {
		return [32]byte{}, fmt.Errorf("invalid Hyperliquid signature chain ID")
	}
	agentAddress, err := addressWord(r.Action.AgentAddress)
	if err != nil {
		return [32]byte{}, fmt.Errorf("invalid Hyperliquid agent address")
	}
	domain := keccak256(
		keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")),
		keccak256([]byte("HyperliquidSignTransaction")),
		keccak256([]byte("1")),
		uintWord(chainID),
		make([]byte, 32),
	)
	message := keccak256(
		keccak256([]byte("HyperliquidTransaction:ApproveAgent(string hyperliquidChain,address agentAddress,string agentName,uint64 nonce)")),
		keccak256([]byte(r.Action.HyperliquidChain)),
		agentAddress,
		keccak256([]byte(r.Action.AgentName)),
		uintWord(uint64(r.Action.Nonce)),
	)
	digest := keccak256([]byte{0x19, 0x01}, domain, message)
	var result [32]byte
	copy(result[:], digest)
	return result, nil
}

func (r ApproveAgentRequest) verifyOwnerSignature() error {
	digest, err := r.SigningHash()
	if err != nil {
		return err
	}
	return verifyEthereumOwner(r.Signature, digest, r.OwnerAddress)
}

func verifyEthereumOwner(signature EthereumSignature, digest [32]byte, ownerAddress string) error {
	rWord, err := scalarWord(signature.R)
	if err != nil {
		return fmt.Errorf("invalid Hyperliquid agent approval signature")
	}
	sWord, err := scalarWord(signature.S)
	if err != nil {
		return fmt.Errorf("invalid Hyperliquid agent approval signature")
	}
	compact := make([]byte, 65)
	compact[0] = byte(signature.V + 4)
	copy(compact[1:33], rWord)
	copy(compact[33:], sWord)
	publicKey, _, err := ecdsa.RecoverCompact(compact, digest[:])
	if err != nil {
		return fmt.Errorf("invalid Hyperliquid owner signature")
	}
	uncompressed := publicKey.SerializeUncompressed()
	addressHash := keccak256(uncompressed[1:])
	recovered := "0x" + hex.EncodeToString(addressHash[len(addressHash)-20:])
	if !strings.EqualFold(recovered, ownerAddress) {
		return fmt.Errorf("Hyperliquid owner signature does not authorize this action")
	}
	return nil
}

func keccak256(parts ...[]byte) []byte {
	hash := sha3.NewLegacyKeccak256()
	for _, part := range parts {
		_, _ = hash.Write(part)
	}
	return hash.Sum(nil)
}

func uintWord(value uint64) []byte {
	word := make([]byte, 32)
	for index := 0; index < 8; index++ {
		word[31-index] = byte(value >> (index * 8))
	}
	return word
}

func addressWord(address string) ([]byte, error) {
	decoded, err := hex.DecodeString(strings.TrimPrefix(address, "0x"))
	if err != nil || len(decoded) != 20 {
		return nil, fmt.Errorf("invalid address")
	}
	word := make([]byte, 32)
	copy(word[12:], decoded)
	return word, nil
}

func scalarWord(value string) ([]byte, error) {
	encoded := strings.TrimPrefix(value, "0x")
	if len(encoded)%2 != 0 {
		encoded = "0" + encoded
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded) > 32 {
		return nil, fmt.Errorf("invalid scalar")
	}
	word := make([]byte, 32)
	copy(word[32-len(decoded):], decoded)
	return word, nil
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
	return a.postApproval(ctx, request.Action, request.Action.Nonce, request.Signature, "agent approval")
}

func (a *AgentApprover) ApproveBuilderFee(ctx context.Context, request ApproveBuilderFeeRequest) error {
	return a.postApproval(ctx, request.Action, request.Action.Nonce, request.Signature, "builder fee approval")
}

func (a *AgentApprover) postApproval(
	ctx context.Context,
	action any,
	nonce int64,
	signature EthereumSignature,
	label string,
) error {
	body, err := json.Marshal(struct {
		Action       any               `json:"action"`
		Nonce        int64             `json:"nonce"`
		Signature    EthereumSignature `json:"signature"`
		VaultAddress *string           `json:"vaultAddress"`
		ExpiresAfter *int64            `json:"expiresAfter"`
	}{
		Action: action, Nonce: nonce, Signature: signature,
	})
	if err != nil {
		return fmt.Errorf("encode Hyperliquid %s: %w", label, err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, agentApprovalTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Hyperliquid %s: %w", label, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := a.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("relay Hyperliquid %s: %w", label, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxApprovalResponse))
	if err != nil {
		return fmt.Errorf("read Hyperliquid %s response: %w", label, err)
	}

	var result struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return fmt.Errorf("invalid Hyperliquid %s response", label)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Status != "ok" {
		return fmt.Errorf("Hyperliquid rejected %s: %s", label, venueApprovalError(result.Response))
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
