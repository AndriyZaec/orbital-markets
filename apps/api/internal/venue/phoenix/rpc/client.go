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
	"sync/atomic"
	"time"

	"github.com/mr-tron/base58"
)

const maxResponseBytes = 1 << 20

var (
	ErrInvalidResponse = errors.New("phoenix RPC invalid response")
	ErrExpired         = errors.New("phoenix transaction blockhash expired")
	ErrSimulation      = errors.New("phoenix transaction simulation failed")
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

// RPCError intentionally omits provider text/data, which may echo credentials
// or transaction payloads. Its code remains available for caller classification.
type RPCError struct {
	Code int `json:"code"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("phoenix RPC error %d", e.Code) }

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
		Error   *RPCError       `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Version != "2.0" || envelope.ID != id {
		return ErrInvalidResponse
	}
	if envelope.Error != nil {
		return envelope.Error
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
	var simulation rpcValue[struct {
		Err json.RawMessage `json:"err"`
	}]
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
		return "", ErrSimulation
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
