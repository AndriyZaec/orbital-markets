package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	// fillTimeout is how long to wait for fills after order acceptance.
	fillTimeout = 15 * time.Second
	// fillPollInterval is how often to check for completion while waiting.
	fillPollInterval = 200 * time.Millisecond
	// fillFullThreshold considers an order fully filled at this ratio.
	fillFullThreshold = 0.995 // 99.5%
)

// trackedOrder holds the evolving state of a submitted order.
type trackedOrder struct {
	mu sync.Mutex

	orderID       string
	clientOrderID string
	symbol        string
	requestedAmt  float64
	submittedAt   time.Time

	status     OrderStatus
	fills      []tradeUpdate
	totalFee   float64
	lastUpdate time.Time
	error      string
}

// tradeUpdate is a single fill event from account_trades.
type tradeUpdate struct {
	Price  float64   `json:"price"`
	Amount float64   `json:"amount"`
	Fee    float64   `json:"fee"`
	At     time.Time `json:"at"`
}

// Tracker correlates live order updates and trades back to submitted orders.
// Fed by the private account WS streams.
type Tracker struct {
	mu     sync.RWMutex
	orders map[string]*trackedOrder // keyed by clientOrderID
	logger *slog.Logger
}

func NewTracker(logger *slog.Logger) *Tracker {
	return &Tracker{
		orders: make(map[string]*trackedOrder),
		logger: logger,
	}
}

// Register starts tracking a submitted order.
func (t *Tracker) Register(submitResult *SubmitResult, requestedAmount float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.orders[submitResult.ClientOrderID] = &trackedOrder{
		orderID:       submitResult.OrderID,
		clientOrderID: submitResult.ClientOrderID,
		symbol:        submitResult.Symbol,
		requestedAmt:  requestedAmount,
		submittedAt:   submitResult.SubmittedAt,
		status:        OrderStatusAccepted,
		lastUpdate:    time.Now(),
	}
}

// HandleOrderUpdate processes an order status update from account_order_updates.
//
// Expected fields: order_id, client_order_id, status, error
func (t *Tracker) HandleOrderUpdate(data json.RawMessage) {
	var update struct {
		OrderID       string `json:"order_id"`
		ClientOrderID string `json:"client_order_id"`
		Status        string `json:"status"`
		Error         string `json:"error"`
	}
	if err := json.Unmarshal(data, &update); err != nil {
		t.logger.Warn("tracker: parse order update", "err", err)
		return
	}

	t.mu.RLock()
	order, exists := t.orders[update.ClientOrderID]
	t.mu.RUnlock()
	if !exists {
		return // not our order
	}

	order.mu.Lock()
	defer order.mu.Unlock()

	if order.orderID == "" && update.OrderID != "" {
		order.orderID = update.OrderID
	}

	switch update.Status {
	case "rejected":
		order.status = OrderStatusRejected
		order.error = update.Error
	case "cancelled":
		order.status = OrderStatusCancelled
	case "expired":
		order.status = OrderStatusExpired
	}

	order.lastUpdate = time.Now()

	t.logger.Info("tracker: order update",
		"client_order_id", update.ClientOrderID,
		"status", update.Status,
	)
}

// HandleTrade processes a fill event from account_trades.
//
// Expected fields: order_id, client_order_id, price, amount, fee, timestamp
func (t *Tracker) HandleTrade(data json.RawMessage) {
	var trade struct {
		OrderID       string      `json:"order_id"`
		ClientOrderID string      `json:"client_order_id"`
		Price         json.Number `json:"price"`
		Amount        json.Number `json:"amount"`
		Fee           json.Number `json:"fee"`
		Timestamp     int64       `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &trade); err != nil {
		t.logger.Warn("tracker: parse trade", "err", err)
		return
	}

	t.mu.RLock()
	order, exists := t.orders[trade.ClientOrderID]
	t.mu.RUnlock()
	if !exists {
		return
	}

	price, _ := trade.Price.Float64()
	amount, _ := trade.Amount.Float64()
	fee, _ := trade.Fee.Float64()

	order.mu.Lock()
	defer order.mu.Unlock()

	fill := tradeUpdate{
		Price:  price,
		Amount: amount,
		Fee:    fee,
		At:     time.UnixMilli(trade.Timestamp),
	}
	order.fills = append(order.fills, fill)
	order.totalFee += fee
	order.lastUpdate = time.Now()

	// Check if fully filled
	filledTotal := 0.0
	for _, f := range order.fills {
		filledTotal += f.Amount
	}
	if order.requestedAmt > 0 && filledTotal/order.requestedAmt >= fillFullThreshold {
		order.status = OrderStatusFilled
	} else {
		order.status = OrderStatusPartialFill
	}

	t.logger.Info("tracker: fill",
		"client_order_id", trade.ClientOrderID,
		"price", price,
		"amount", amount,
		"total_filled", filledTotal,
		"status", order.status,
	)
}

// WaitForFill blocks until the order reaches a terminal state or times out.
func (t *Tracker) WaitForFill(ctx context.Context, clientOrderID string) (*FillResult, error) {
	deadline := time.Now().Add(fillTimeout)

	for {
		if time.Now().After(deadline) {
			return t.buildResult(clientOrderID, OrderStatusTimeout)
		}

		select {
		case <-ctx.Done():
			return t.buildResult(clientOrderID, OrderStatusTimeout)
		case <-time.After(fillPollInterval):
		}

		t.mu.RLock()
		order, exists := t.orders[clientOrderID]
		t.mu.RUnlock()
		if !exists {
			return nil, fmt.Errorf("order not tracked: %s", clientOrderID)
		}

		order.mu.Lock()
		status := order.status
		order.mu.Unlock()

		if status.IsTerminal() {
			return t.buildResult(clientOrderID, status)
		}
	}
}

func (t *Tracker) buildResult(clientOrderID string, finalStatus OrderStatus) (*FillResult, error) {
	t.mu.RLock()
	order, exists := t.orders[clientOrderID]
	t.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("order not tracked: %s", clientOrderID)
	}

	order.mu.Lock()
	defer order.mu.Unlock()

	if !finalStatus.IsTerminal() && order.status.IsTerminal() {
		finalStatus = order.status
	}
	if finalStatus == OrderStatusTimeout && len(order.fills) > 0 {
		finalStatus = OrderStatusPartialFill
	}

	var filledAmount, weightedPrice float64
	var firstFill, lastFill *time.Time

	for i, f := range order.fills {
		filledAmount += f.Amount
		weightedPrice += f.Price * f.Amount
		if i == 0 {
			t := f.At
			firstFill = &t
		}
		t := f.At
		lastFill = &t
	}

	var avgPrice float64
	if filledAmount > 0 {
		avgPrice = weightedPrice / filledAmount
	}

	return &FillResult{
		OrderID:         order.orderID,
		ClientOrderID:   clientOrderID,
		Symbol:          order.symbol,
		Status:          finalStatus,
		RequestedAmount: order.requestedAmt,
		FilledAmount:    filledAmount,
		AvgFillPrice:    avgPrice,
		FillCount:       len(order.fills),
		TotalFee:        order.totalFee,
		SubmittedAt:     order.submittedAt,
		FirstFillAt:     firstFill,
		LastFillAt:      lastFill,
		ResolvedAt:      time.Now(),
		Error:           order.error,
	}, nil
}

// Cleanup removes terminal orders older than maxAge.
func (t *Tracker) Cleanup(maxAge time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for id, order := range t.orders {
		order.mu.Lock()
		if order.status.IsTerminal() && order.lastUpdate.Before(cutoff) {
			delete(t.orders, id)
		}
		order.mu.Unlock()
	}
}
