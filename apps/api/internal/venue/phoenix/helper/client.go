package helper

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	maxRequestBytes  = 64 * 1_024
	maxResponseBytes = 256 * 1_024
	maxStderrBytes   = 8 * 1_024
)

type Client struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	responses      chan readResult
	waitDone       chan error
	stderr         *boundedBuffer
	requestTimeout time.Duration
	callSlot       chan struct{}

	stateMu   sync.Mutex
	stopped   bool
	exitErr   error
	stopOnce  sync.Once
	closeOnce sync.Once
}

type readResult struct {
	data []byte
	err  error
}

func Start(ctx context.Context, path string, requestTimeout time.Duration) (*Client, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("phoenix helper path is required")
	}
	return startClient(ctx, exec.Command(path), requestTimeout)
}

func startClient(ctx context.Context, cmd *exec.Cmd, requestTimeout time.Duration) (*Client, error) {
	if requestTimeout <= 0 {
		return nil, errors.New("phoenix helper request timeout must be positive")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create phoenix helper stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create phoenix helper stdout: %w", err)
	}
	stderr := &boundedBuffer{max: maxStderrBytes}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start phoenix helper: %w", err)
	}

	client := &Client{
		cmd:            cmd,
		stdin:          stdin,
		responses:      make(chan readResult, 1),
		waitDone:       make(chan error, 1),
		stderr:         stderr,
		requestTimeout: requestTimeout,
		callSlot:       make(chan struct{}, 1),
	}
	go client.readResponses(stdout)
	go func() {
		err := cmd.Wait()
		client.stateMu.Lock()
		client.stopped = true
		client.exitErr = err
		client.stateMu.Unlock()
		client.waitDone <- err
	}()

	health, err := client.Health(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("phoenix helper health handshake: %w", err)
	}
	if health.HelperVersion == "" || health.PhoenixRiseVersion != PhoenixRiseVersion {
		client.Close()
		return nil, fmt.Errorf(
			"incompatible phoenix helper: helper_version=%q phoenix_rise_version=%q, expected phoenix-rise %s",
			health.HelperVersion,
			health.PhoenixRiseVersion,
			PhoenixRiseVersion,
		)
	}
	return client, nil
}

func (c *Client) Health(ctx context.Context) (HealthResult, error) {
	var result HealthResult
	err := c.call(ctx, "health", nil, &result)
	return result, err
}

func (c *Client) BuildOrder(ctx context.Context, request BuildOrderRequest) (BuildOrderResult, error) {
	var result BuildOrderResult
	if err := c.call(ctx, "build_order", request, &result); err != nil {
		return BuildOrderResult{}, err
	}
	if err := validateBuildOrderResult(request, result); err != nil {
		c.stop()
		return BuildOrderResult{}, fmt.Errorf("invalid phoenix helper build_order result: %w", err)
	}
	return result, nil
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.stop()
		<-c.waitDone
	})
}

func (c *Client) call(ctx context.Context, operation string, payload any, result any) error {
	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	select {
	case <-requestCtx.Done():
		return requestCtx.Err()
	case c.callSlot <- struct{}{}:
	}
	defer func() { <-c.callSlot }()

	c.stateMu.Lock()
	stopped := c.stopped
	exitErr := c.exitErr
	c.stateMu.Unlock()
	if stopped {
		if exitErr != nil {
			return fmt.Errorf("phoenix helper is stopped: %w%s", exitErr, c.stderrSuffix())
		}
		return errors.New("phoenix helper is stopped")
	}

	requestID := uuid.NewString()
	frame, err := json.Marshal(request{
		ProtocolVersion: ProtocolVersion,
		RequestID:       requestID,
		Operation:       operation,
		Payload:         payload,
	})
	if err != nil {
		return fmt.Errorf("encode phoenix helper request: %w", err)
	}
	if len(frame)+1 > maxRequestBytes {
		return fmt.Errorf("phoenix helper request exceeds %d bytes", maxRequestBytes)
	}
	if err := requestCtx.Err(); err != nil {
		return err
	}
	frame = append(frame, '\n')
	writeDone := make(chan error, 1)
	go func() {
		written, err := c.stdin.Write(frame)
		if err == nil && written != len(frame) {
			err = fmt.Errorf("wrote %d of %d bytes", written, len(frame))
		}
		writeDone <- err
	}()
	select {
	case <-requestCtx.Done():
		c.stop()
		return requestCtx.Err()
	case err := <-writeDone:
		if err == nil {
			break
		}
		c.stop()
		return fmt.Errorf("write phoenix helper request: %w%s", err, c.stderrSuffix())
	}

	select {
	case <-requestCtx.Done():
		c.stop()
		return requestCtx.Err()
	case read := <-c.responses:
		if read.err != nil {
			c.stop()
			return fmt.Errorf("read phoenix helper response: %w%s", read.err, c.stderrSuffix())
		}
		err := decodeResponse(read.data, requestID, result)
		var protocolErr *ProtocolError
		if err != nil && !errors.As(err, &protocolErr) {
			c.stop()
		}
		return err
	}
}

func (c *Client) readResponses(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4_096), maxResponseBytes+1)
	for scanner.Scan() {
		frame := append([]byte(nil), scanner.Bytes()...)
		c.responses <- readResult{data: frame}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.responses <- readResult{err: err}
}

func (c *Client) stop() {
	c.stopOnce.Do(func() {
		c.stateMu.Lock()
		c.stopped = true
		c.stateMu.Unlock()
		_ = c.stdin.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}

func (c *Client) stderrSuffix() string {
	stderr := strings.TrimSpace(c.stderr.String())
	if stderr == "" {
		return ""
	}
	return ": stderr: " + stderr
}

func decodeResponse(frame []byte, requestID string, result any) error {
	var response response
	if err := decodeStrict(frame, &response); err != nil {
		return fmt.Errorf("decode phoenix helper response: %w", err)
	}
	if response.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("phoenix helper protocol version %d, expected %d", response.ProtocolVersion, ProtocolVersion)
	}
	if response.RequestID != requestID {
		return fmt.Errorf("phoenix helper request ID %q, expected %q", response.RequestID, requestID)
	}
	if !response.OK {
		if response.Error == nil || response.Error.Code == "" || response.Error.Message == "" {
			return errors.New("phoenix helper returned an invalid error response")
		}
		return response.Error
	}
	if response.Error != nil || len(response.Result) == 0 {
		return errors.New("phoenix helper returned an invalid success response")
	}
	if err := decodeStrict(response.Result, result); err != nil {
		return fmt.Errorf("decode phoenix helper result: %w", err)
	}
	return nil
}

func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func validateBuildOrderResult(request BuildOrderRequest, result BuildOrderResult) error {
	message, err := base64.StdEncoding.DecodeString(result.MessageBase64)
	if err != nil || len(message) == 0 {
		return errors.New("message_base64 is invalid")
	}
	digest := sha256.Sum256(message)
	if !strings.EqualFold(result.MessageSHA256, hex.EncodeToString(digest[:])) {
		return errors.New("message_sha256 does not match message_base64")
	}
	if result.RecentBlockhash != request.RecentBlockhash ||
		result.LastValidBlockHeight != request.LastValidBlockHeight ||
		result.MetadataGeneration != request.MetadataGeneration {
		return errors.New("blockhash validity or metadata generation does not match request")
	}
	if result.TraderPDAIndex != 0 || result.SubaccountIndex != 0 {
		return errors.New("helper returned a non-cross-margin trader account")
	}
	if result.TraderAccount == "" ||
		result.Manifest.Format != "solana_legacy_message" ||
		result.Manifest.PhoenixProgramID != PhoenixProgramID ||
		result.Manifest.IntentAction != request.Action ||
		result.Manifest.OwnerAuthority != request.OwnerAuthority ||
		result.Manifest.PositionAuthority != request.PositionAuthority ||
		result.Manifest.FeePayer != request.FeePayer ||
		result.Manifest.TraderAccount != result.TraderAccount ||
		result.Manifest.RequestedSymbol != request.Symbol ||
		result.Manifest.Orderbook != request.Accounts.Orderbook ||
		result.Manifest.MetadataGeneration != request.MetadataGeneration ||
		result.Manifest.Side != request.Side ||
		result.Manifest.NumBaseLots != request.NumBaseLots ||
		result.Manifest.PriceLimitTicks != request.PriceLimitTicks ||
		result.Manifest.ReduceOnly != request.ReduceOnly ||
		result.Manifest.ClientOrderID != request.ClientOrderID ||
		result.Manifest.LastValidSlot != request.LastValidSlot ||
		len(result.Manifest.AccountKeys) == 0 ||
		len(result.Manifest.Instructions) == 0 {
		return errors.New("manifest does not match build_order request")
	}
	if len(result.RequiredSigners) != 2 ||
		result.RequiredSigners[0] != (RequiredSigner{Index: 0, Pubkey: request.FeePayer, Role: "fee_payer"}) ||
		result.RequiredSigners[1] != (RequiredSigner{Index: 1, Pubkey: request.PositionAuthority, Role: "position_authority"}) {
		return errors.New("required_signers do not match fee payer and position authority")
	}
	switch request.Action {
	case ActionOpen, ActionClose:
		flight := result.Manifest.Flight
		if result.Manifest.Routing != "flight" ||
			flight == nil ||
			flight.FlightProgramID != FlightProgramID ||
			flight.BuilderAuthority != request.BuilderAuthority ||
			flight.BuilderTraderAccount == "" ||
			flight.BuilderState == "" ||
			flight.BuilderPDAIndex != 0 ||
			flight.BuilderSubaccountIndex != 0 ||
			flight.ExpectedRegisteredFeeBPS != ExpectedFlightFeeBPS ||
			flight.UsesFeeOverride {
			return errors.New("normal order does not contain the expected Flight routing")
		}
	case ActionUnwind, ActionEmergencyClose:
		if result.Manifest.Routing != "native" || result.Manifest.Flight != nil {
			return errors.New("safety order must bypass Flight")
		}
	default:
		return fmt.Errorf("unsupported action %q", request.Action)
	}
	return nil
}

type boundedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		if len(data) < remaining {
			remaining = len(data)
		}
		_, _ = b.buf.Write(data[:remaining])
	}
	return len(data), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
