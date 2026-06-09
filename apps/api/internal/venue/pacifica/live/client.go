package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
)

const (
	tradingWSURL   = "wss://ws.pacifica.fi/ws"
	submitTimeout  = 10 * time.Second
	expiryWindowMs = 5_000 // 5s signature expiry (matches SDK default)
)

// Signer produces signatures for trading payloads.
// Pacifica signing: ed25519 signMessage on canonical JSON bytes → base58 signature.
type Signer interface {
	Account() string
	Sign(payload []byte) (string, error) // returns base58-encoded signature
}

// Client submits live market orders to Pacifica.
// Signer is optional — required only for custodial SubmitMarketOrder/SubmitCloseOrder.
// The non-custodial SubmitSignedOrder path works without a signer.
type Client struct {
	signer       Signer // nil in non-custodial mode
	accountState *account.AccountState
	logger       *slog.Logger

	mu   sync.Mutex
	conn *websocket.Conn
}

func NewClient(
	logger *slog.Logger,
	signer Signer,
	accountState *account.AccountState,
) *Client {
	return &Client{
		signer:       signer,
		accountState: accountState,
		logger:       logger,
	}
}

// SubmitMarketOrder validates and submits a market order for one leg.
//
// This does NOT confirm fills — only that the venue accepted/rejected the order.
func (c *Client) SubmitMarketOrder(
	ctx context.Context,
	symbol string,
	side domain.Side,
	amount float64,
	leverage float64,
	marginRequired float64,
	clientOrderID string,
) (*SubmitResult, error) {
	if c.signer == nil {
		return nil, fmt.Errorf("custodial submit requires a signer — use SubmitSignedOrder for non-custodial flow")
	}

	// Pre-trade validation
	snap := c.accountState.Snapshot()
	validation := account.ValidatePreTrade(snap, symbol, marginRequired, leverage)
	if !validation.CanProceed() {
		c.logger.Warn("pacifica live: pre-trade blocked", "symbol", symbol, "reasons", validation.Reasons)
		return &SubmitResult{
			ClientOrderID: clientOrderID, Symbol: symbol, Accepted: false,
			Error: fmt.Sprintf("pre-trade blocked: %v", validation.Reasons),
			SubmittedAt: time.Now(), RespondedAt: time.Now(),
		}, nil
	}
	if validation.Level == account.ValidationWarning {
		c.logger.Warn("pacifica live: pre-trade warnings", "symbol", symbol, "reasons", validation.Reasons)
	}

	pacSide := sideToVenue(side)
	amountStr := fmt.Sprintf("%g", amount)
	cloid := ensureUUID(clientOrderID)

	req, err := c.buildAndSign(symbol, pacSide, amountStr, false, "0.5", cloid)
	if err != nil {
		return nil, err
	}

	c.logger.Info("pacifica live: submitting order",
		"symbol", symbol, "side", pacSide, "amount", amountStr, "client_order_id", cloid)

	result, err := c.sendOrder(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("submit order: %w", err)
	}

	logOutcome(c.logger, "pacifica", result)
	return result, nil
}

// SubmitCloseOrder submits a reduce-only market order to close/unwind a position leg.
// Side is inverted: closing a long = ask, closing a short = bid.
func (c *Client) SubmitCloseOrder(
	ctx context.Context,
	symbol string,
	positionSide domain.Side,
	amount float64,
	clientOrderID string,
) (*SubmitResult, error) {
	if c.signer == nil {
		return nil, fmt.Errorf("custodial submit requires a signer — use SubmitSignedOrder for non-custodial flow")
	}

	snap := c.accountState.Snapshot()
	if !snap.Connected {
		return &SubmitResult{
			ClientOrderID: clientOrderID, Symbol: symbol, Accepted: false,
			Error: "account stream not connected", SubmittedAt: time.Now(), RespondedAt: time.Now(),
		}, nil
	}
	if snap.LastUpdated.IsZero() || time.Since(snap.LastUpdated) > 30*time.Second {
		return &SubmitResult{
			ClientOrderID: clientOrderID, Symbol: symbol, Accepted: false,
			Error: fmt.Sprintf("account state stale (%.0fs)", time.Since(snap.LastUpdated).Seconds()),
			SubmittedAt: time.Now(), RespondedAt: time.Now(),
		}, nil
	}

	// Invert side: close long = ask, close short = bid
	closeSide := "ask"
	if positionSide == domain.SideShort {
		closeSide = "bid"
	}

	amountStr := fmt.Sprintf("%g", amount)
	cloid := ensureUUID(clientOrderID)

	req, err := c.buildAndSign(symbol, closeSide, amountStr, true, "1.0", cloid)
	if err != nil {
		return nil, err
	}

	c.logger.Info("pacifica live: submitting close order",
		"symbol", symbol, "close_side", closeSide, "amount", amountStr, "reduce_only", true, "client_order_id", cloid)

	result, err := c.sendOrder(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("submit close order: %w", err)
	}

	logOutcome(c.logger, "pacifica", result)
	return result, nil
}

// buildAndSign constructs the canonical signing message, signs it, and returns the final request.
func (c *Client) buildAndSign(
	symbol, side, amount string,
	reduceOnly bool,
	slippagePct string,
	clientOrderID string,
) (MarketOrderRequest, error) {
	now := time.Now()
	timestamp := now.UnixMilli()

	// Build the data portion of the signing payload
	data := BuildMarketOrderSigningData(symbol, side, amount, reduceOnly, slippagePct, clientOrderID)

	// Build the canonical signing message (sorted keys, compact JSON)
	signingBytes, err := BuildSigningMessage("create_market_order", timestamp, expiryWindowMs, data)
	if err != nil {
		return MarketOrderRequest{}, fmt.Errorf("build signing message: %w", err)
	}

	// Sign with Solana signMessage → base58
	sig, err := c.signer.Sign(signingBytes)
	if err != nil {
		return MarketOrderRequest{}, fmt.Errorf("sign: %w", err)
	}

	return MarketOrderRequest{
		Account:       c.signer.Account(),
		Signature:     sig,
		Timestamp:     timestamp,
		ExpiryWindow:  expiryWindowMs,
		Symbol:        symbol,
		Side:          side,
		Amount:        amount,
		ReduceOnly:    reduceOnly,
		SlippagePct:   slippagePct,
		ClientOrderID: clientOrderID,
	}, nil
}

// sendOrder sends the order via WebSocket using the correct Pacifica envelope
// and waits for the response.
//
// Pacifica envelope: {"id": "uuid", "params": {"create_market_order": {...}}}
// Response: {"code": 200, "data": {"I": "cloid", "i": oid, "s": "BTC"}, "id": "uuid", "t": ms, "type": "..."}
func (c *Client) sendOrder(ctx context.Context, req MarketOrderRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, tradingWSURL, nil)
		if err != nil {
			return nil, fmt.Errorf("dial: %w", err)
		}
		c.conn = conn
	}

	// Pacifica WS envelope
	envelope := WSEnvelope{
		ID: uuid.New().String(),
		Params: map[string]any{
			"create_market_order": req,
		},
	}

	submittedAt := time.Now()
	if err := c.conn.WriteJSON(envelope); err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("write: %w", err)
	}

	deadline := time.Now().Add(submitTimeout)
	c.conn.SetReadDeadline(deadline)

	_, raw, err := c.conn.ReadMessage()
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("read response: %w", err)
	}
	c.conn.SetReadDeadline(time.Time{})

	respondedAt := time.Now()

	// Parse Pacifica response:
	// {"code": 200, "data": {"I": "cloid", "i": 645953, "s": "BTC"}, "id": "uuid", "t": ms, "type": "..."}
	var resp struct {
		Code int    `json:"code"`
		ID   string `json:"id"`
		Type string `json:"type"`
		T    int64  `json:"t"`
		Data struct {
			I string `json:"I"` // client order ID (CLOID)
			OrderID  int64  `json:"i"` // venue order ID
			S string `json:"s"` // symbol
		} `json:"data"`
		// Error responses may have different shapes — code != 200 is rejection
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w (raw: %s)", err, string(raw[:min(len(raw), 200)]))
	}

	accepted := resp.Code == 200
	orderID := ""
	if resp.Data.OrderID > 0 {
		orderID = fmt.Sprintf("%d", resp.Data.OrderID)
	}

	errMsg := resp.Error
	if !accepted && errMsg == "" {
		errMsg = fmt.Sprintf("code %d", resp.Code)
	}

	return &SubmitResult{
		RequestID:     resp.ID,
		OrderID:       orderID,
		ClientOrderID: req.ClientOrderID,
		Symbol:        req.Symbol,
		Accepted:      accepted,
		Error:         errMsg,
		SubmittedAt:   submittedAt,
		RespondedAt:   respondedAt,
	}, nil
}

// Close cleanly shuts down the trading connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// sideToVenue converts domain side to Pacifica venue side.
func sideToVenue(s domain.Side) string {
	if s == domain.SideLong {
		return "bid"
	}
	return "ask"
}

// ensureUUID returns a valid UUID string. If the input is not a UUID, generates a new one.
func ensureUUID(s string) string {
	if _, err := uuid.Parse(s); err == nil {
		return s
	}
	return uuid.New().String()
}

func logOutcome(logger *slog.Logger, venue string, result *SubmitResult) {
	if result.Accepted {
		logger.Info(venue+" live: order accepted",
			"order_id", result.OrderID, "client_order_id", result.ClientOrderID)
	} else {
		logger.Warn(venue+" live: order rejected",
			"client_order_id", result.ClientOrderID, "error", result.Error)
	}
}
