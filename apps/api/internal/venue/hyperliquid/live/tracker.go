package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsURL            = "wss://api.hyperliquid.xyz/ws"
	fillTimeout      = 15 * time.Second
	fillPollInterval = 200 * time.Millisecond
	fillFullThreshold = 0.995
	reconnectDelay   = 5 * time.Second
)

// trackedOrder holds the evolving state of a submitted order.
type trackedOrder struct {
	mu            sync.Mutex
	orderID       string
	clientOrderID string
	symbol        string
	requestedAmt  float64
	submittedAt   time.Time
	status        OrderStatus
	filledAmt     float64
	avgPrice      float64
	totalFee      float64
	fills         []fillEvent
	resolvedAt    time.Time
	error         string
}

type fillEvent struct {
	Price  float64
	Amount float64
	Fee    float64
	At     time.Time
}

// Tracker subscribes to Hyperliquid WS orderUpdates and correlates fills.
type Tracker struct {
	mu       sync.RWMutex
	byOID    map[string]*trackedOrder // keyed by orderID
	byCloid  map[string]*trackedOrder // keyed by clientOrderID
	address  string
	logger   *slog.Logger
}

func NewTracker(logger *slog.Logger, address string) *Tracker {
	return &Tracker{
		byOID:   make(map[string]*trackedOrder),
		byCloid: make(map[string]*trackedOrder),
		address: address,
		logger:  logger,
	}
}

// Register starts tracking a submitted order.
func (t *Tracker) Register(result *SubmitResult, requestedAmount float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	order := &trackedOrder{
		orderID:       result.OrderID,
		clientOrderID: result.ClientOrderID,
		symbol:        result.Symbol,
		requestedAmt:  requestedAmount,
		submittedAt:   result.SubmittedAt,
		status:        OrderStatusAccepted,
	}

	if result.OrderID != "" {
		t.byOID[result.OrderID] = order
	}
	if result.ClientOrderID != "" {
		t.byCloid[result.ClientOrderID] = order
	}
}

// Run connects to WS and listens for orderUpdates until ctx is cancelled.
func (t *Tracker) Run(ctx context.Context) {
	for {
		err := t.connectAndListen(ctx)
		if ctx.Err() != nil {
			return
		}
		t.logger.Error("hyperliquid order ws disconnected, reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

func (t *Tracker) connectAndListen(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Subscribe to orderUpdates + userFills
	for _, subType := range []string{"orderUpdates", "userFills"} {
		sub := map[string]any{
			"method": "subscribe",
			"subscription": map[string]string{
				"type": subType,
				"user": t.address,
			},
		}
		if err := conn.WriteJSON(sub); err != nil {
			return fmt.Errorf("subscribe %s: %w", subType, err)
		}
	}

	t.logger.Info("hyperliquid order tracker ws connected")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var msg struct {
			Channel string          `json:"channel"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Channel {
		case "orderUpdates":
			t.handleOrderUpdates(msg.Data)
		case "userFills":
			t.handleUserFills(msg.Data)
		}
	}
}

// WsOrder matches Hyperliquid's WS orderUpdates shape.
type WsOrder struct {
	Order struct {
		Coin      string  `json:"coin"`
		Side      string  `json:"side"`
		LimitPx   string  `json:"limitPx"`
		Sz        string  `json:"sz"`      // remaining size
		OID       int64   `json:"oid"`
		Timestamp int64   `json:"timestamp"`
		OrigSz    string  `json:"origSz"`
		Cloid     *string `json:"cloid"` // client order ID, may be null
	} `json:"order"`
	Status         string `json:"status"` // "open", "filled", "canceled", "triggered", "rejected", "marginCanceled"
	StatusTimestamp int64  `json:"statusTimestamp"`
}

func (t *Tracker) handleOrderUpdates(data json.RawMessage) {
	var orders []WsOrder
	if err := json.Unmarshal(data, &orders); err != nil {
		t.logger.Warn("hyperliquid tracker: parse orderUpdates", "err", err)
		return
	}

	for _, wsOrder := range orders {
		order := t.findOrder(wsOrder)
		if order == nil {
			continue
		}

		order.mu.Lock()

		// Backfill orderID if we matched by cloid
		oid := fmt.Sprintf("%d", wsOrder.Order.OID)
		if order.orderID == "" && oid != "0" {
			order.orderID = oid
			t.mu.Lock()
			t.byOID[oid] = order
			t.mu.Unlock()
		}

		// Compute filled amount from origSz - remaining sz
		origSz, _ := strconv.ParseFloat(wsOrder.Order.OrigSz, 64)
		remainSz, _ := strconv.ParseFloat(wsOrder.Order.Sz, 64)
		filledAmt := origSz - remainSz
		if filledAmt > 0 {
			order.filledAmt = filledAmt
		}

		// Map Hyperliquid status to Orbital status
		switch wsOrder.Status {
		case "filled":
			order.status = OrderStatusFilled
			order.resolvedAt = time.Now()
		case "canceled", "marginCanceled":
			if order.filledAmt > 0 {
				order.status = OrderStatusPartialFill
			} else {
				order.status = OrderStatusCancelled
			}
			order.resolvedAt = time.Now()
		case "rejected":
			order.status = OrderStatusRejected
			order.resolvedAt = time.Now()
		case "open", "triggered":
			if order.filledAmt > 0 && order.requestedAmt > 0 &&
				order.filledAmt/order.requestedAmt >= fillFullThreshold {
				order.status = OrderStatusFilled
				order.resolvedAt = time.Now()
			} else if order.filledAmt > 0 {
				order.status = OrderStatusPartialFill
			}
		}

		order.mu.Unlock()

		t.logger.Info("hyperliquid tracker: order update",
			"oid", oid,
			"hl_status", wsOrder.Status,
			"filled", filledAmt,
			"orbital_status", order.status,
		)
	}
}

// handleUserFills processes fill events from the userFills WS stream.
// Provides price and fee data that orderUpdates doesn't carry.
func (t *Tracker) handleUserFills(data json.RawMessage) {
	var payload struct {
		IsSnapshot bool `json:"isSnapshot"`
		Fills      []struct {
			Coin    string  `json:"coin"`
			Px      string  `json:"px"`
			Sz      string  `json:"sz"`
			Side    string  `json:"side"`
			Time    int64   `json:"time"`
			Fee     string  `json:"fee"`
			OID     int64   `json:"oid"`
			Cloid   *string `json:"cloid"`
		} `json:"fills"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.logger.Warn("hyperliquid tracker: parse userFills", "err", err)
		return
	}

	// Skip initial snapshot — only process live fills
	if payload.IsSnapshot {
		return
	}

	for _, f := range payload.Fills {
		oid := fmt.Sprintf("%d", f.OID)

		t.mu.RLock()
		order, exists := t.byOID[oid]
		if !exists && f.Cloid != nil && *f.Cloid != "" {
			order, exists = t.byCloid[*f.Cloid]
		}
		t.mu.RUnlock()
		if !exists {
			continue
		}

		px, _ := strconv.ParseFloat(f.Px, 64)
		sz, _ := strconv.ParseFloat(f.Sz, 64)
		fee, _ := strconv.ParseFloat(f.Fee, 64)

		order.mu.Lock()
		order.fills = append(order.fills, fillEvent{
			Price:  px,
			Amount: sz,
			Fee:    fee,
			At:     time.UnixMilli(f.Time),
		})

		// Recompute avg price and total fee from fills
		var totalQty, totalValue, totalFee float64
		for _, fill := range order.fills {
			totalQty += fill.Amount
			totalValue += fill.Price * fill.Amount
			totalFee += fill.Fee
		}
		if totalQty > 0 {
			order.avgPrice = totalValue / totalQty
		}
		order.totalFee = totalFee
		order.mu.Unlock()

		t.logger.Debug("hyperliquid tracker: fill",
			"oid", oid,
			"price", px,
			"size", sz,
			"fee", fee,
		)
	}
}

// findOrder looks up a tracked order by OID or cloid.
func (t *Tracker) findOrder(ws WsOrder) *trackedOrder {
	t.mu.RLock()
	defer t.mu.RUnlock()

	oid := fmt.Sprintf("%d", ws.Order.OID)
	if order, ok := t.byOID[oid]; ok {
		return order
	}
	if ws.Order.Cloid != nil && *ws.Order.Cloid != "" {
		if order, ok := t.byCloid[*ws.Order.Cloid]; ok {
			return order
		}
	}
	return nil
}

// WaitForFill blocks until the order reaches a terminal state or times out.
func (t *Tracker) WaitForFill(ctx context.Context, clientOrderID string) (*FillResult, error) {
	t.mu.RLock()
	order, exists := t.byCloid[clientOrderID]
	t.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("order not tracked: %s", clientOrderID)
	}

	deadline := time.Now().Add(fillTimeout)

	for {
		if time.Now().After(deadline) {
			return t.buildResultFromOrder(order, OrderStatusTimeout), nil
		}

		select {
		case <-ctx.Done():
			return t.buildResultFromOrder(order, OrderStatusTimeout), nil
		case <-time.After(fillPollInterval):
		}

		order.mu.Lock()
		status := order.status
		order.mu.Unlock()

		if status.IsTerminal() {
			return t.buildResultFromOrder(order, status), nil
		}
	}
}

func (t *Tracker) buildResultFromOrder(order *trackedOrder, finalStatus OrderStatus) *FillResult {

	order.mu.Lock()
	defer order.mu.Unlock()

	if !finalStatus.IsTerminal() && order.status.IsTerminal() {
		finalStatus = order.status
	}
	if finalStatus == OrderStatusTimeout && order.filledAmt > 0 {
		finalStatus = OrderStatusPartialFill
	}

	resolvedAt := order.resolvedAt
	if resolvedAt.IsZero() {
		resolvedAt = time.Now()
	}

	var firstFill, lastFill *time.Time
	if len(order.fills) > 0 {
		t0 := order.fills[0].At
		firstFill = &t0
		tN := order.fills[len(order.fills)-1].At
		lastFill = &tN
	}

	return &FillResult{
		OrderID:         order.orderID,
		ClientOrderID:   order.clientOrderID,
		Symbol:          order.symbol,
		Status:          finalStatus,
		RequestedAmount: order.requestedAmt,
		FilledAmount:    order.filledAmt,
		AvgFillPrice:    order.avgPrice,
		FillCount:       len(order.fills),
		TotalFee:        order.totalFee,
		SubmittedAt:     order.submittedAt,
		FirstFillAt:     firstFill,
		LastFillAt:      lastFill,
		ResolvedAt:      resolvedAt,
		Error:           order.error,
	}
}
