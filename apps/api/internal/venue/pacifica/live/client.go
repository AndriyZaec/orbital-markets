package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
)

const (
	tradingWSURL   = "wss://ws.pacifica.fi/ws"
	submitTimeout  = 10 * time.Second
	expiryWindowMs = 30_000 // 30s order expiry
)

// Signer produces signatures for trading payloads.
// Concrete implementation depends on wallet/key management.
type Signer interface {
	Account() string
	Sign(payload []byte) (string, error)
}

// Client submits live market orders to Pacifica.
type Client struct {
	signer       Signer
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
// Flow:
//  1. Pre-trade validation
//  2. Build signed payload
//  3. Send via WS
//  4. Wait for response
//  5. Return structured result
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
	// 1. Pre-trade validation
	snap := c.accountState.Snapshot()
	validation := account.ValidatePreTrade(snap, symbol, marginRequired, leverage)

	if !validation.CanProceed() {
		c.logger.Warn("pacifica live: pre-trade blocked",
			"symbol", symbol,
			"reasons", validation.Reasons,
		)
		return &SubmitResult{
			ClientOrderID: clientOrderID,
			Symbol:        symbol,
			Accepted:      false,
			Error:         fmt.Sprintf("pre-trade blocked: %v", validation.Reasons),
			SubmittedAt:   time.Now(),
			RespondedAt:   time.Now(),
		}, nil
	}

	if validation.Level == account.ValidationWarning {
		c.logger.Warn("pacifica live: pre-trade warnings",
			"symbol", symbol,
			"reasons", validation.Reasons,
		)
	}

	// 2. Build order payload
	pacSide := "buy"
	if side == domain.SideShort {
		pacSide = "sell"
	}

	now := time.Now()
	req := MarketOrderRequest{
		Account:       c.signer.Account(),
		Timestamp:     now.UnixMilli(),
		ExpiryWindow:  expiryWindowMs,
		Symbol:        symbol,
		Side:          pacSide,
		Amount:        amount,
		ReduceOnly:    false,
		SlippagePct:   0.5, // 0.5% default slippage tolerance
		ClientOrderID: clientOrderID,
	}

	// Sign the payload
	payloadBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal order: %w", err)
	}

	sig, err := c.signer.Sign(payloadBytes)
	if err != nil {
		return nil, fmt.Errorf("sign order: %w", err)
	}
	req.Signature = sig

	c.logger.Info("pacifica live: submitting order",
		"symbol", symbol,
		"side", pacSide,
		"amount", amount,
		"client_order_id", clientOrderID,
	)

	// 3. Send via WS
	result, err := c.sendOrder(ctx, req)
	if err != nil {
		c.logger.Error("pacifica live: submit failed",
			"symbol", symbol,
			"err", err,
		)
		return nil, fmt.Errorf("submit order: %w", err)
	}

	// 5. Log outcome
	if result.Accepted {
		c.logger.Info("pacifica live: order accepted",
			"symbol", symbol,
			"order_id", result.OrderID,
			"client_order_id", result.ClientOrderID,
		)
	} else {
		c.logger.Warn("pacifica live: order rejected",
			"symbol", symbol,
			"client_order_id", result.ClientOrderID,
			"error", result.Error,
		)
	}

	return result, nil
}

// sendOrder sends the order via WebSocket and waits for the response.
func (c *Client) sendOrder(ctx context.Context, req MarketOrderRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Ensure connection
	if c.conn == nil {
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, tradingWSURL, nil)
		if err != nil {
			return nil, fmt.Errorf("dial: %w", err)
		}
		c.conn = conn
	}

	// Send
	submitMsg := map[string]any{
		"method": "create_market_order",
		"params": req,
	}

	submittedAt := time.Now()
	if err := c.conn.WriteJSON(submitMsg); err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("write: %w", err)
	}

	// Wait for response with timeout
	deadline := time.Now().Add(submitTimeout)
	c.conn.SetReadDeadline(deadline)

	_, raw, err := c.conn.ReadMessage()
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("read response: %w", err)
	}
	c.conn.SetReadDeadline(time.Time{}) // clear deadline

	respondedAt := time.Now()

	// Parse response
	var resp struct {
		Channel string `json:"channel"`
		Data    struct {
			RequestID string `json:"request_id"`
			OrderID   string `json:"order_id"`
			Status    string `json:"status"` // "accepted" or "rejected"
			Error     string `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w (raw: %s)", err, string(raw[:min(len(raw), 200)]))
	}

	return &SubmitResult{
		RequestID:     resp.Data.RequestID,
		OrderID:       resp.Data.OrderID,
		ClientOrderID: req.ClientOrderID,
		Symbol:        req.Symbol,
		Accepted:      resp.Data.Status == "accepted",
		Error:         resp.Data.Error,
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
