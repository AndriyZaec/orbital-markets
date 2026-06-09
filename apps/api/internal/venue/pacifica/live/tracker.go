package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
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
// Pacifica format (array of updates):
//
//	[{
//	  "i":  1559665358,        // order ID (integer)
//	  "I":  "uuid" or null,    // client order ID (nullable)
//	  "s":  "BTC",             // symbol
//	  "d":  "bid",             // side
//	  "a":  "0.00012",         // original amount
//	  "f":  "0.00012",         // filled amount
//	  "os": "filled",          // order status
//	  "oe": "fulfill_limit",   // order event
//	  "ot": "limit",           // order type
//	  "p":  "89501",           // avg filled price
//	  "r":  false,             // reduce_only
//	  "ct": 1765017049008,     // created timestamp ms
//	  "ut": 1765017219639,     // updated timestamp ms
//	}]
func (t *Tracker) HandleOrderUpdate(data json.RawMessage) {
	var updates []struct {
		I  int64   `json:"i"`  // order ID
		CI *string `json:"I"`  // client order ID (nullable)
		S  string  `json:"s"`  // symbol
		OS string  `json:"os"` // order status
		F  string  `json:"f"`  // filled amount
		A  string  `json:"a"`  // original amount
		P  string  `json:"p"`  // avg fill price
	}
	if err := json.Unmarshal(data, &updates); err != nil {
		t.logger.Warn("pacifica tracker: parse order update", "err", err)
		return
	}

	for _, u := range updates {
		// Find tracked order by client order ID
		var cloid string
		if u.CI != nil {
			cloid = *u.CI
		}
		if cloid == "" {
			continue // can't correlate without client order ID
		}

		t.mu.RLock()
		order, exists := t.orders[cloid]
		t.mu.RUnlock()
		if !exists {
			continue
		}

		order.mu.Lock()

		// Backfill venue order ID
		oid := fmt.Sprintf("%d", u.I)
		if order.orderID == "" && u.I > 0 {
			order.orderID = oid
		}

		// Map Pacifica order status to Orbital status
		switch u.OS {
		case "filled":
			order.status = OrderStatusFilled
		case "cancelled", "canceled":
			filledAmt := parseFloat(u.F)
			if filledAmt > 0 {
				order.status = OrderStatusPartialFill
			} else {
				order.status = OrderStatusCancelled
			}
		case "rejected":
			order.status = OrderStatusRejected
		case "expired":
			order.status = OrderStatusExpired
		case "open", "partial_fill":
			filledAmt := parseFloat(u.F)
			if filledAmt > 0 && order.requestedAmt > 0 &&
				filledAmt/order.requestedAmt >= fillFullThreshold {
				order.status = OrderStatusFilled
			} else if filledAmt > 0 {
				order.status = OrderStatusPartialFill
			}
		}

		order.lastUpdate = time.Now()
		order.mu.Unlock()

		t.logger.Info("pacifica tracker: order update",
			"cloid", cloid,
			"oid", oid,
			"status", u.OS,
			"filled", u.F,
			"orbital_status", order.status,
		)
	}
}

// HandleTrade processes a fill event from account_trades.
//
// Pacifica format (array of trades):
//
//	[{
//	  "h":  80063441,      // history ID
//	  "i":  1559912767,    // order ID
//	  "I":  "uuid"/null,   // client order ID
//	  "s":  "BTC",         // symbol
//	  "p":  "89477",       // fill price
//	  "a":  "0.00036",     // trade amount
//	  "f":  "0.012885",    // fee
//	  "n":  "-0.022965",   // realized PnL
//	  "t":  1765018588190, // timestamp ms
//	}]
func (t *Tracker) HandleTrade(data json.RawMessage) {
	var trades []struct {
		I  int64   `json:"i"`  // order ID
		CI *string `json:"I"`  // client order ID (nullable)
		S  string  `json:"s"`  // symbol
		P  string  `json:"p"`  // price
		A  string  `json:"a"`  // amount
		F  string  `json:"f"`  // fee
		T  int64   `json:"t"`  // timestamp ms
	}
	if err := json.Unmarshal(data, &trades); err != nil {
		t.logger.Warn("pacifica tracker: parse trade", "err", err)
		return
	}

	for _, tr := range trades {
		var cloid string
		if tr.CI != nil {
			cloid = *tr.CI
		}
		if cloid == "" {
			// Try to find by order ID
			oid := fmt.Sprintf("%d", tr.I)
			t.mu.RLock()
			for _, o := range t.orders {
				if o.orderID == oid {
					cloid = o.clientOrderID
					break
				}
			}
			t.mu.RUnlock()
		}
		if cloid == "" {
			continue
		}

		t.mu.RLock()
		order, exists := t.orders[cloid]
		t.mu.RUnlock()
		if !exists {
			continue
		}

		price := parseFloat(tr.P)
		amount := parseFloat(tr.A)
		fee := parseFloat(tr.F)

		order.mu.Lock()
		fill := tradeUpdate{
			Price:  price,
			Amount: amount,
			Fee:    fee,
			At:     time.UnixMilli(tr.T),
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
		} else if filledTotal > 0 {
			order.status = OrderStatusPartialFill
		}
		order.mu.Unlock()

		t.logger.Info("pacifica tracker: fill",
			"cloid", cloid,
			"price", price,
			"amount", amount,
			"total_filled", filledTotal,
			"status", order.status,
		)
	}
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

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
