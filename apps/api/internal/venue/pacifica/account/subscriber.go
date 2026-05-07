package account

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

const (
	privateWSURL = "wss://ws.pacifica.fi/ws"
	reconnectDelay = 5 * time.Second
)

// StreamHandler receives raw channel data for order/trade events.
type StreamHandler interface {
	HandleOrderUpdate(data json.RawMessage)
	HandleTrade(data json.RawMessage)
}

// Subscriber connects to Pacifica's private WebSocket and keeps AccountState updated.
// Requires an API key or auth token for private streams.
type Subscriber struct {
	state   *AccountState
	apiKey  string
	handler StreamHandler // optional: receives order/trade updates
	logger  *slog.Logger
}

func NewSubscriber(
	logger *slog.Logger,
	state *AccountState,
	apiKey string,
	handler StreamHandler,
) *Subscriber {
	return &Subscriber{
		state:   state,
		apiKey:  apiKey,
		handler: handler,
		logger:  logger,
	}
}

// Run connects and listens to private account streams until ctx is cancelled.
func (s *Subscriber) Run(ctx context.Context) {
	for {
		err := s.connectAndListen(ctx)
		if ctx.Err() != nil {
			return
		}
		s.state.SetConnected(false)
		s.logger.Error("pacifica account ws disconnected, reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

// wsMessage is the raw envelope from private streams.
type wsMessage struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

func (s *Subscriber) connectAndListen(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, privateWSURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Authenticate
	if err := conn.WriteJSON(map[string]any{
		"method": "auth",
		"params": map[string]string{
			"token": s.apiKey,
		},
	}); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	// Subscribe to private account channels
	channels := []string{
		"account_info",
		"account_positions",
		"account_margin",
		"account_leverage",
		"account_order_updates",
		"account_trades",
	}
	for _, ch := range channels {
		if err := conn.WriteJSON(map[string]any{
			"method": "subscribe",
			"params": map[string]string{"source": ch},
		}); err != nil {
			return fmt.Errorf("subscribe %s: %w", ch, err)
		}
	}

	s.state.SetConnected(true)
	s.logger.Info("pacifica account ws connected", "channels", len(channels))

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

		var msg wsMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		s.handleMessage(msg)
	}
}

func (s *Subscriber) handleMessage(msg wsMessage) {
	switch msg.Channel {
	case "account_info":
		s.handleAccountInfo(msg.Data)
	case "account_positions":
		s.handlePositions(msg.Data)
	case "account_margin":
		s.handleMargin(msg.Data)
	case "account_leverage":
		s.handleLeverage(msg.Data)
	case "account_order_updates":
		if s.handler != nil {
			s.handler.HandleOrderUpdate(msg.Data)
		}
	case "account_trades":
		if s.handler != nil {
			s.handler.HandleTrade(msg.Data)
		}
	}
}

// handleAccountInfo processes account equity and balance updates.
// Expected format (approximate — adapt when Pacifica docs confirm):
//
//	{"equity": "12345.67", "available": "8000.00", "withdrawable": "7500.00"}
func (s *Subscriber) handleAccountInfo(data json.RawMessage) {
	var info struct {
		Equity       json.Number `json:"equity"`
		Available    json.Number `json:"available"`
		Withdrawable json.Number `json:"withdrawable"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		s.logger.Warn("pacifica: parse account_info", "err", err)
		return
	}

	equity, _ := info.Equity.Float64()
	available, _ := info.Available.Float64()
	withdrawable, _ := info.Withdrawable.Float64()

	snap := s.state.Snapshot()
	s.state.UpdateEquity(equity, available, withdrawable, snap.TotalMarginUsed, snap.MaintenanceMargin)
}

// handleMargin processes margin usage updates.
func (s *Subscriber) handleMargin(data json.RawMessage) {
	var margin struct {
		TotalUsed   json.Number `json:"total_used"`
		Maintenance json.Number `json:"maintenance"`
	}
	if err := json.Unmarshal(data, &margin); err != nil {
		s.logger.Warn("pacifica: parse account_margin", "err", err)
		return
	}

	totalUsed, _ := margin.TotalUsed.Float64()
	maintenance, _ := margin.Maintenance.Float64()

	snap := s.state.Snapshot()
	s.state.UpdateEquity(
		snap.Equity, snap.AvailableToSpend, snap.AvailableToWithdraw,
		totalUsed, maintenance,
	)
}

// handlePositions processes full position snapshot.
func (s *Subscriber) handlePositions(data json.RawMessage) {
	var positions []struct {
		Symbol        string      `json:"symbol"`
		Side          string      `json:"side"`
		Size          json.Number `json:"size"`
		EntryPrice    json.Number `json:"entry_price"`
		MarkPrice     json.Number `json:"mark_price"`
		UnrealizedPnL json.Number `json:"unrealized_pnl"`
		Leverage      json.Number `json:"leverage"`
		MarginUsed    json.Number `json:"margin_used"`
		LiqPrice      json.Number `json:"liq_price"`
	}
	if err := json.Unmarshal(data, &positions); err != nil {
		s.logger.Warn("pacifica: parse account_positions", "err", err)
		return
	}

	var parsed []AccountPosition
	for _, p := range positions {
		size, _ := p.Size.Float64()
		entry, _ := p.EntryPrice.Float64()
		mark, _ := p.MarkPrice.Float64()
		pnl, _ := p.UnrealizedPnL.Float64()
		lev, _ := p.Leverage.Float64()
		margin, _ := p.MarginUsed.Float64()
		liq, _ := p.LiqPrice.Float64()

		parsed = append(parsed, AccountPosition{
			Symbol:        p.Symbol,
			Side:          p.Side,
			Size:          size,
			EntryPrice:    entry,
			MarkPrice:     mark,
			UnrealizedPnL: pnl,
			Leverage:      lev,
			MarginUsed:    margin,
			LiqPrice:      liq,
		})
	}

	s.state.UpdatePositions(parsed)
}

// handleLeverage processes per-symbol leverage/margin mode updates.
func (s *Subscriber) handleLeverage(data json.RawMessage) {
	var configs []struct {
		Symbol     string      `json:"symbol"`
		Leverage   json.Number `json:"leverage"`
		MarginMode string      `json:"margin_mode"`
	}
	if err := json.Unmarshal(data, &configs); err != nil {
		s.logger.Warn("pacifica: parse account_leverage", "err", err)
		return
	}

	for _, c := range configs {
		lev, _ := c.Leverage.Float64()
		mode := MarginModeUnknown
		switch c.MarginMode {
		case "cross":
			mode = MarginModeCross
		case "isolated":
			mode = MarginModeIsolated
		}
		s.state.UpdateSymbolConfig(SymbolConfig{
			Symbol:     c.Symbol,
			Leverage:   lev,
			MarginMode: mode,
		})
	}
}
