package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/account"
)

const (
	exchangeURL   = "https://api.hyperliquid.xyz/exchange"
	submitTimeout = 10 * time.Second
)

// Signer produces EIP-712 typed data signatures for Hyperliquid.
// Hyperliquid uses Ethereum-style signing (not Solana).
type Signer interface {
	Address() string
	SignAction(action any, nonce int64) (string, error)
}

// AssetMap resolves symbol names to Hyperliquid asset indices.
type AssetMap interface {
	AssetIndex(symbol string) (int, bool)
}

// Client submits live orders to Hyperliquid via REST.
// Signer is optional — required only for custodial SubmitMarketOrder/SubmitCloseOrder.
// The non-custodial SubmitSignedOrder path works without a signer.
type Client struct {
	signer       Signer // nil in non-custodial mode
	assetMap     AssetMap
	accountState *account.AccountState
	httpClient   *http.Client
	tracker      *Tracker
	logger       *slog.Logger
}

func NewClient(
	logger *slog.Logger,
	signer Signer,
	assetMap AssetMap,
	accountState *account.AccountState,
	tracker *Tracker,
) *Client {
	return &Client{
		signer:       signer,
		assetMap:     assetMap,
		accountState: accountState,
		httpClient:   &http.Client{Timeout: submitTimeout},
		tracker:      tracker,
		logger:       logger,
	}
}

// SubmitMarketOrder validates and submits a market order for one leg.
func (c *Client) SubmitMarketOrder(
	ctx context.Context,
	symbol string,
	side domain.Side,
	amount float64,
	price float64,
	leverage float64,
	marginRequired float64,
	clientOrderID string,
) (*SubmitResult, error) {
	// Signer required for custodial path
	if c.signer == nil {
		return nil, fmt.Errorf("custodial submit requires a signer — use SubmitSignedOrder for non-custodial flow")
	}

	// 1. Pre-trade validation
	snap := c.accountState.Snapshot()
	validation := account.ValidatePreTrade(snap, symbol, marginRequired, leverage)

	if !validation.CanProceed() {
		c.logger.Warn("hyperliquid live: pre-trade blocked",
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
		c.logger.Warn("hyperliquid live: pre-trade warnings",
			"symbol", symbol,
			"reasons", validation.Reasons,
		)
	}

	// 2. Build order
	return c.submitOrder(ctx, symbol, side, amount, price, false, clientOrderID)
}

// SubmitCloseOrder submits a reduce-only market order to close/unwind.
// Side is inverted: closing a long = sell, closing a short = buy.
func (c *Client) SubmitCloseOrder(
	ctx context.Context,
	symbol string,
	positionSide domain.Side,
	amount float64,
	price float64,
	clientOrderID string,
) (*SubmitResult, error) {
	// Signer required for custodial path
	if c.signer == nil {
		return nil, fmt.Errorf("custodial submit requires a signer — use SubmitSignedOrder for non-custodial flow")
	}

	// Check connectivity only (no margin check for close)
	snap := c.accountState.Snapshot()
	if !snap.Connected {
		return &SubmitResult{
			ClientOrderID: clientOrderID,
			Symbol:        symbol,
			Accepted:      false,
			Error:         "account state not connected",
			SubmittedAt:   time.Now(),
			RespondedAt:   time.Now(),
		}, nil
	}

	// Invert side for close
	closeSide := domain.SideLong
	if positionSide == domain.SideLong {
		closeSide = domain.SideShort
	}

	c.logger.Info("hyperliquid live: submitting close order",
		"symbol", symbol,
		"position_side", positionSide,
		"close_side", closeSide,
		"amount", amount,
		"client_order_id", clientOrderID,
	)

	return c.submitOrder(ctx, symbol, closeSide, amount, price, true, clientOrderID)
}

// WaitForFill delegates to the tracker.
func (c *Client) WaitForFill(ctx context.Context, clientOrderID string) (*FillResult, error) {
	return c.tracker.WaitForFill(ctx, clientOrderID)
}

func (c *Client) submitOrder(
	ctx context.Context,
	symbol string,
	side domain.Side,
	amount float64,
	price float64,
	reduceOnly bool,
	clientOrderID string,
) (*SubmitResult, error) {
	assetIdx, ok := c.assetMap.AssetIndex(symbol)
	if !ok {
		return &SubmitResult{
			ClientOrderID: clientOrderID,
			Symbol:        symbol,
			Accepted:      false,
			Error:         fmt.Sprintf("unknown asset: %s", symbol),
			SubmittedAt:   time.Now(),
			RespondedAt:   time.Now(),
		}, nil
	}

	isBuy := side == domain.SideLong

	// Hyperliquid IOC market order: set limit price with slippage, TIF=Ioc
	// For buy: price * 1.005 (0.5% above), for sell: price * 0.995
	slippageMul := 1.005
	if !isBuy {
		slippageMul = 0.995
	}
	limitPx := fmt.Sprintf("%.6f", price*slippageMul)

	// cloid is a 128-bit hex string for client-side order tracking
	cloid := fmt.Sprintf("0x%032x", time.Now().UnixNano())

	action := OrderAction{
		Type: "order",
		Orders: []OrderSpec{{
			Asset:      assetIdx,
			IsBuy:      isBuy,
			LimitPx:    limitPx,
			Size:       fmt.Sprintf("%.6f", amount),
			ReduceOnly: reduceOnly,
			OrderType:  OrderType{Limit: LimitSpec{Tif: "Ioc"}},
			Cloid:      cloid,
		}},
		Grouping: "na",
	}

	nonce := time.Now().UnixMilli()
	sig, err := c.signer.SignAction(action, nonce)
	if err != nil {
		return nil, fmt.Errorf("sign action: %w", err)
	}

	// Build request body
	reqBody := map[string]any{
		"action":    action,
		"nonce":     nonce,
		"signature": sig,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	c.logger.Info("hyperliquid live: submitting order",
		"symbol", symbol,
		"side", side,
		"amount", amount,
		"reduce_only", reduceOnly,
		"client_order_id", clientOrderID,
	)

	submittedAt := time.Now()

	// 3. POST to exchange
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("hyperliquid live: submit failed", "symbol", symbol, "err", err)
		return nil, fmt.Errorf("submit: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	respondedAt := time.Now()

	// 4. Parse response
	result := c.parseResponse(respBody, symbol, cloid, submittedAt, respondedAt)

	// Register with tracker if accepted
	if result.Accepted && c.tracker != nil {
		c.tracker.Register(result, amount)
	}

	// 5. Log
	if result.Accepted {
		c.logger.Info("hyperliquid live: order accepted",
			"symbol", symbol,
			"order_id", result.OrderID,
			"client_order_id", clientOrderID,
		)
	} else {
		c.logger.Warn("hyperliquid live: order rejected",
			"symbol", symbol,
			"client_order_id", clientOrderID,
			"error", result.Error,
		)
	}

	return result, nil
}

// parseResponse handles Hyperliquid exchange response.
// Success: {"status": "ok", "response": {"type": "order", "data": {"statuses": [...]}}}
// Error: {"status": "err", "response": "error message"}
func (c *Client) parseResponse(
	body []byte,
	symbol, clientOrderID string,
	submittedAt, respondedAt time.Time,
) *SubmitResult {
	var raw struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return &SubmitResult{
			ClientOrderID: clientOrderID,
			Symbol:        symbol,
			Accepted:      false,
			Error:         fmt.Sprintf("parse error: %s", string(body[:min(len(body), 200)])),
			SubmittedAt:   submittedAt,
			RespondedAt:   respondedAt,
		}
	}

	if raw.Status != "ok" {
		errMsg := string(raw.Response)
		return &SubmitResult{
			ClientOrderID: clientOrderID,
			Symbol:        symbol,
			Accepted:      false,
			Error:         errMsg,
			SubmittedAt:   submittedAt,
			RespondedAt:   respondedAt,
		}
	}

	// Parse order response
	var orderResp struct {
		Type string `json:"type"`
		Data struct {
			Statuses []json.RawMessage `json:"statuses"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw.Response, &orderResp); err != nil {
		return &SubmitResult{
			ClientOrderID: clientOrderID,
			Symbol:        symbol,
			Accepted:      true, // status was ok
			SubmittedAt:   submittedAt,
			RespondedAt:   respondedAt,
		}
	}

	// Extract order result from first status
	// IOC filled: {"filled": {"totalSz": "0.02", "avgPx": "1891.4", "oid": 77747314}}
	// Resting:    {"resting": {"oid": 77738308}}
	// Error:      {"error": "Order must have minimum value of $10."}
	var orderID string
	if len(orderResp.Data.Statuses) > 0 {
		var status struct {
			Resting struct {
				OID int64 `json:"oid"`
			} `json:"resting"`
			Filled struct {
				TotalSz string `json:"totalSz"`
				AvgPx   string `json:"avgPx"`
				OID     int64  `json:"oid"`
			} `json:"filled"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(orderResp.Data.Statuses[0], &status); err == nil {
			if status.Error != "" {
				return &SubmitResult{
					ClientOrderID: clientOrderID,
					Symbol:        symbol,
					Accepted:      false,
					Error:         status.Error,
					SubmittedAt:   submittedAt,
					RespondedAt:   respondedAt,
				}
			}
			if status.Filled.OID > 0 {
				orderID = fmt.Sprintf("%d", status.Filled.OID)
			} else if status.Resting.OID > 0 {
				orderID = fmt.Sprintf("%d", status.Resting.OID)
			}
		}
	}

	return &SubmitResult{
		OrderID:       orderID,
		ClientOrderID: clientOrderID,
		Symbol:        symbol,
		Accepted:      true,
		SubmittedAt:   submittedAt,
		RespondedAt:   respondedAt,
	}
}
