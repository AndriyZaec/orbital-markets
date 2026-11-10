// Package rpc provides the bounded Solana JSON-RPC operations used by Phoenix.
// It owns no keys and does not infer Phoenix fills from RPC acceptance/status.
package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mr-tron/base58"
)

const maxResponseBytes = 1 << 20

var (
	ErrInvalidResponse = errors.New("phoenix RPC invalid response")
	ErrExpired         = errors.New("phoenix transaction blockhash expired")
	ErrSimulation      = errors.New("phoenix transaction simulation failed")
	ErrAccountNotFound = errors.New("Solana account not found")
)

type Client struct {
	endpoint string
	http     *http.Client
	nextID   atomic.Uint64
}

func New(endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid Phoenix RPC endpoint")
	}
	return &Client{endpoint: endpoint, http: &http.Client{
		Timeout: 5 * time.Second,
		// A redirect must not forward a credential-bearing RPC request elsewhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// RPCError omits provider text and arbitrary data, which may echo credentials
// or transaction payloads. Only allowlisted simulation evidence is kept privately.
type RPCError struct {
	Code       int `json:"code"`
	simulation *SimulationError
}

func (e *RPCError) Error() string { return fmt.Sprintf("phoenix RPC error %d", e.Code) }

func (e *RPCError) Diagnostic() string {
	if e.simulation != nil {
		return e.Error() + ": " + e.simulation.Diagnostic()
	}
	return e.Error()
}

// SimulationError keeps the actual program failure separate from safe public copy.
// Errors.Is continues to classify it as ErrSimulation.
type SimulationError struct {
	Slot          uint64          `json:"slot,omitempty"`
	Err           json.RawMessage `json:"err"`
	Logs          []string        `json:"logs,omitempty"`
	UnitsConsumed *uint64         `json:"unitsConsumed,omitempty"`
}

func (*SimulationError) Error() string { return ErrSimulation.Error() }
func (*SimulationError) Unwrap() error { return ErrSimulation }
func (e *SimulationError) Diagnostic() string {
	raw, _ := json.Marshal(e)
	return ErrSimulation.Error() + ": " + string(raw)
}

func (c *Client) redactSimulation(e *SimulationError) {
	// Provider logs must not reintroduce a credential-bearing endpoint into logs.
	u, _ := url.Parse(c.endpoint)
	redact := func(value string) string {
		value = strings.ReplaceAll(value, c.endpoint, "[RPC endpoint]")
		for _, values := range u.Query() {
			for _, secret := range values {
				if secret != "" {
					value = strings.ReplaceAll(value, url.QueryEscape(secret), "[redacted]")
					value = strings.ReplaceAll(value, secret, "[redacted]")
				}
			}
		}
		return value
	}
	for i, log := range e.Logs {
		e.Logs[i] = redact(log)
	}
	clean := redact(string(e.Err))
	if json.Valid([]byte(clean)) {
		e.Err = json.RawMessage(clean)
	} else {
		e.Err, _ = json.Marshal(clean)
	}
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	body, err := json.Marshal(struct {
		Version string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("phoenix RPC request construction failed")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		// net/http's url.Error includes the Helius API key in its URL.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("phoenix RPC %s transport: %w", method, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("phoenix RPC %s HTTP %d", method, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("phoenix RPC response read failed")
	}
	if len(raw) > maxResponseBytes {
		return ErrInvalidResponse
	}
	var envelope struct {
		Version string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Version != "2.0" || envelope.ID != id {
		return ErrInvalidResponse
	}
	if envelope.Error != nil {
		rpcErr := &RPCError{Code: envelope.Error.Code}
		if method == "simulateTransaction" || method == "sendTransaction" {
			var evidence SimulationError
			if json.Unmarshal(envelope.Error.Data, &evidence) == nil && len(evidence.Err) > 0 && !bytes.Equal(bytes.TrimSpace(evidence.Err), []byte("null")) {
				c.redactSimulation(&evidence)
				rpcErr.simulation = &evidence
			}
		}
		return rpcErr
	}
	if len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) || json.Unmarshal(envelope.Result, out) != nil {
		return ErrInvalidResponse
	}
	return nil
}

type rpcValue[T any] struct {
	Context struct {
		Slot uint64 `json:"slot"`
	} `json:"context"`
	Value *T `json:"value"`
}

type Blockhash struct {
	Hash                 string `json:"blockhash"`
	LastValidBlockHeight uint64 `json:"lastValidBlockHeight"`
	Slot                 uint64 `json:"-"`
}

func validBase58(value string, size int) bool {
	if len(value) > size*2 {
		return false
	}
	decoded, err := base58.Decode(value)
	return err == nil && len(decoded) == size
}

func (c *Client) LatestBlockhash(ctx context.Context) (Blockhash, error) {
	var result rpcValue[Blockhash]
	if err := c.call(ctx, "getLatestBlockhash", []any{map[string]string{"commitment": "confirmed"}}, &result); err != nil {
		return Blockhash{}, err
	}
	if result.Value == nil || result.Context.Slot == 0 || !validBase58(result.Value.Hash, 32) || result.Value.LastValidBlockHeight == 0 {
		return Blockhash{}, ErrInvalidResponse
	}
	result.Value.Slot = result.Context.Slot
	return *result.Value, nil
}

func (c *Client) BlockHeight(ctx context.Context, minSlot uint64) (uint64, error) {
	var height uint64
	err := c.call(ctx, "getBlockHeight", []any{map[string]any{"commitment": "confirmed", "minContextSlot": minSlot}}, &height)
	if err == nil && height == 0 {
		err = ErrInvalidResponse
	}
	return height, err
}

type BlockhashValidity struct {
	Valid bool
	Slot  uint64
}

// FinalizedBlockhashValidity binds invalidity proof to its bank slot. Requiring
// at least the preparation slot avoids mistaking a not-yet-finalized new hash
// for an expired one. Authority reads must use at least the returned slot.
func (c *Client) FinalizedBlockhashValidity(ctx context.Context, hash string, minSlot uint64) (BlockhashValidity, error) {
	if !validBase58(hash, 32) {
		return BlockhashValidity{}, ErrInvalidResponse
	}
	var result rpcValue[bool]
	if err := c.call(ctx, "isBlockhashValid", []any{hash, map[string]any{"commitment": "finalized", "minContextSlot": minSlot}}, &result); err != nil {
		return BlockhashValidity{}, err
	}
	if result.Value == nil || result.Context.Slot < minSlot || result.Context.Slot == 0 {
		return BlockhashValidity{}, ErrInvalidResponse
	}
	return BlockhashValidity{Valid: *result.Value, Slot: result.Context.Slot}, nil
}

type AccountInfo struct {
	Slot       uint64
	Owner      string
	DataBase64 string
}

// AccountInfo reads standard Solana account state. finalized is used when
// proving that an unknown, expired submission can no longer execute.
func (c *Client) AccountInfo(ctx context.Context, address string, minSlot uint64, finalized bool) (AccountInfo, error) {
	if !validBase58(address, 32) {
		return AccountInfo{}, errors.New("invalid account address")
	}
	commitment := "confirmed"
	if finalized {
		commitment = "finalized"
	}
	var result rpcValue[struct {
		Owner      string   `json:"owner"`
		Executable *bool    `json:"executable"`
		Data       []string `json:"data"`
	}]
	if err := c.call(ctx, "getAccountInfo", []any{address, map[string]any{"encoding": "base64", "commitment": commitment, "minContextSlot": minSlot}}, &result); err != nil {
		return AccountInfo{}, err
	}
	if result.Context.Slot == 0 || result.Context.Slot < minSlot {
		return AccountInfo{}, ErrInvalidResponse
	}
	if result.Value == nil {
		return AccountInfo{}, ErrAccountNotFound
	}
	v := result.Value
	if v.Executable == nil || *v.Executable || !validBase58(v.Owner, 32) || len(v.Data) != 2 || v.Data[1] != "base64" || len(v.Data[0]) > base64.StdEncoding.EncodedLen(32<<10) {
		return AccountInfo{}, ErrInvalidResponse
	}
	if raw, err := base64.StdEncoding.DecodeString(v.Data[0]); err != nil || len(raw) == 0 || len(raw) > 32<<10 {
		return AccountInfo{}, ErrInvalidResponse
	}
	return AccountInfo{Slot: result.Context.Slot, Owner: v.Owner, DataBase64: v.Data[0]}, nil
}

// Balance returns lamports (integer units), including a valid zero balance.
func (c *Client) Balance(ctx context.Context, address string) (uint64, error) {
	if !validBase58(address, 32) {
		return 0, errors.New("invalid relayer address")
	}
	var result rpcValue[uint64]
	if err := c.call(ctx, "getBalance", []any{address, map[string]string{"commitment": "confirmed"}}, &result); err != nil {
		return 0, err
	}
	if result.Value == nil || result.Context.Slot == 0 {
		return 0, ErrInvalidResponse
	}
	return *result.Value, nil
}

// FeeForMessage quotes the total network fee (including priority fees) for the
// exact serialized message. A null fee means its blockhash is unavailable;
// never replace that with a zero-fee estimate or a different blockhash.
func (c *Client) FeeForMessage(ctx context.Context, message []byte, minSlot uint64) (uint64, error) {
	if len(message) == 0 || len(message) > 1232 {
		return 0, errors.New("invalid transaction message")
	}
	var result rpcValue[uint64]
	err := c.call(ctx, "getFeeForMessage", []any{base64.StdEncoding.EncodeToString(message), map[string]any{
		"commitment": "confirmed", "minContextSlot": minSlot,
	}}, &result)
	if err != nil {
		return 0, err
	}
	if result.Value == nil || *result.Value == 0 || result.Context.Slot == 0 || result.Context.Slot < minSlot {
		return 0, ErrInvalidResponse
	}
	return *result.Value, nil
}

type SignatureStatus struct {
	Slot               uint64          `json:"slot"`
	Err                json.RawMessage `json:"err"`
	ConfirmationStatus string          `json:"confirmationStatus"`
}

// SignatureStatus returns nil, nil for an unknown signature, never for proof
// that a transaction failed to land. Even finalized success is not a Phoenix fill.
func (c *Client) SignatureStatus(ctx context.Context, signature string) (*SignatureStatus, error) {
	if !validBase58(signature, 64) {
		return nil, errors.New("invalid transaction signature")
	}
	var result rpcValue[[]*SignatureStatus]
	if err := c.call(ctx, "getSignatureStatuses", []any{[]string{signature}, map[string]bool{"searchTransactionHistory": true}}, &result); err != nil {
		return nil, err
	}
	if result.Value == nil || result.Context.Slot == 0 || len(*result.Value) != 1 {
		return nil, ErrInvalidResponse
	}
	status := (*result.Value)[0]
	if status != nil {
		if status.Slot == 0 || len(status.Err) == 0 {
			return nil, ErrInvalidResponse
		}
		switch status.ConfirmationStatus {
		case "", "processed", "confirmed", "finalized":
		default:
			return nil, ErrInvalidResponse
		}
	}
	return status, nil
}

// AmbiguousSendError retains the locally known transaction ID for reconciliation.
// Do not retry with a new transaction while this signature may have landed.
type AmbiguousSendError struct {
	Signature string
	Cause     error
}

func (e *AmbiguousSendError) Error() string {
	return "phoenix transaction submission uncertain: " + e.Cause.Error()
}
func (e *AmbiguousSendError) Unwrap() error { return e.Cause }

// SimulateAndSend accepts a fully signed packet from the trusted relayer path.
// The caller owns intent/signature authorization, fee policy and durable state.
// This method checks expiry, verifies signatures in simulation, then sends the
// identical bytes once. A returned signature means RPC acceptance only.
func (c *Client) SimulateAndSend(ctx context.Context, transaction []byte, lastValidHeight, minSlot uint64) (string, error) {
	// Legacy Solana packets are at most 1232 bytes; any fitting nonzero signature
	// count fits one shortvec byte. Do not implement message/instruction decoding here.
	if len(transaction) == 0 || len(transaction) > 1232 || transaction[0] == 0 || transaction[0] >= 128 || len(transaction) <= 1+int(transaction[0])*64 || lastValidHeight == 0 {
		return "", errors.New("invalid signed transaction packet")
	}
	encoded := base64.StdEncoding.EncodeToString(transaction)
	signature := base58.Encode(transaction[1:65])
	checkHeight := func() error {
		height, err := c.BlockHeight(ctx, minSlot)
		if err != nil {
			return err
		}
		if height > lastValidHeight {
			return ErrExpired
		}
		return nil
	}
	if err := checkHeight(); err != nil {
		return "", err
	}
	var simulation rpcValue[SimulationError]
	if err := c.call(ctx, "simulateTransaction", []any{encoded, map[string]any{
		"encoding": "base64", "commitment": "confirmed", "sigVerify": true,
		"replaceRecentBlockhash": false, "minContextSlot": minSlot,
	}}, &simulation); err != nil {
		return "", err
	}
	if simulation.Value == nil || simulation.Context.Slot < minSlot || simulation.Context.Slot == 0 || len(simulation.Value.Err) == 0 {
		return "", ErrInvalidResponse
	}
	if !bytes.Equal(simulation.Value.Err, []byte("null")) {
		simulation.Value.Slot = simulation.Context.Slot
		c.redactSimulation(simulation.Value)
		return "", simulation.Value
	}
	if err := checkHeight(); err != nil {
		return "", err
	}
	var accepted string
	err := c.call(ctx, "sendTransaction", []any{encoded, map[string]any{
		"encoding": "base64", "skipPreflight": false, "preflightCommitment": "confirmed",
		"maxRetries": 0, "minContextSlot": minSlot,
	}}, &accepted)
	if err == nil && accepted != signature {
		err = ErrInvalidResponse
	}
	if err != nil {
		return signature, &AmbiguousSendError{Signature: signature, Cause: err}
	}
	return signature, nil
}
