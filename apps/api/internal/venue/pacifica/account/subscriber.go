package account

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

const (
	privateWSURL   = "wss://ws.pacifica.fi/ws"
	reconnectDelay = 5 * time.Second
)

// StreamHandler receives raw channel data for order/trade events.
type StreamHandler interface {
	HandleOrderUpdate(data json.RawMessage)
	HandleTrade(data json.RawMessage)
}

// Subscriber connects to Pacifica's WebSocket and keeps AccountState updated.
//
// Pacifica account subscriptions require the account's public key (not an API key).
// Each subscription message includes the account address.
// No separate auth/login step is needed — subscriptions are public by account address.
type Subscriber struct {
	state   *AccountState
	account string         // Solana public key (base58)
	handler StreamHandler  // optional: receives order/trade updates
	logger  *slog.Logger
}

func NewSubscriber(
	logger *slog.Logger,
	state *AccountState,
	account string,
	handler StreamHandler,
) *Subscriber {
	return &Subscriber{
		state:   state,
		account: account,
		handler: handler,
		logger:  logger,
	}
}

// Run connects and listens to account streams until ctx is cancelled.
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

	// Subscribe to account channels — each needs the account address.
	// No separate auth step is required.
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
			"params": map[string]string{
				"source":  ch,
				"account": s.account,
			},
		}); err != nil {
			return fmt.Errorf("subscribe %s: %w", ch, err)
		}
	}

	s.state.SetConnected(true)
	s.logger.Info("pacifica account ws connected",
		"channels", len(channels),
		"account", s.account,
	)

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
		s.handleMarginMode(msg.Data)
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
	case "subscribe":
		// Subscription confirmation — ignore
	}
}

// handleAccountInfo processes account equity and balance updates.
//
// Pacifica format:
//
//	{
//	  "ae": "2000",      // account equity
//	  "as": "1500",      // available to spend
//	  "aw": "1400",      // available to withdraw
//	  "b":  "2000",      // account balance
//	  "mu": "500",       // total margin used
//	  "cm": "400",       // maintenance margin (cross mode)
//	  "t":  1234567890   // timestamp ms
//	}
func (s *Subscriber) handleAccountInfo(data json.RawMessage) {
	var info struct {
		AE string `json:"ae"` // account equity
		AS string `json:"as"` // available to spend
		AW string `json:"aw"` // available to withdraw
		MU string `json:"mu"` // total margin used
		CM string `json:"cm"` // maintenance margin
	}
	if err := json.Unmarshal(data, &info); err != nil {
		s.logger.Warn("pacifica: parse account_info", "err", err)
		return
	}

	equity := parseFloat(info.AE)
	available := parseFloat(info.AS)
	withdrawable := parseFloat(info.AW)
	marginUsed := parseFloat(info.MU)
	maintenance := parseFloat(info.CM)

	s.state.UpdateEquity(equity, available, withdrawable, marginUsed, maintenance)
}

// handleMarginMode processes per-symbol margin mode changes (isolated/cross).
//
// Pacifica format:
//
//	{
//	  "u": "42trU9A5...",  // account
//	  "s": "ETH",          // symbol
//	  "i": true,           // isolated mode (true = isolated, false = cross)
//	  "t": 1234567890      // timestamp ms
//	}
//
// Note: this is NOT account-level margin totals — those come from account_info.
func (s *Subscriber) handleMarginMode(data json.RawMessage) {
	var update struct {
		S string `json:"s"` // symbol
		I bool   `json:"i"` // isolated mode
	}
	if err := json.Unmarshal(data, &update); err != nil {
		s.logger.Warn("pacifica: parse account_margin", "err", err)
		return
	}

	mode := MarginModeCross
	if update.I {
		mode = MarginModeIsolated
	}

	// Update the symbol config's margin mode, preserving existing leverage
	snap := s.state.Snapshot()
	existing, ok := snap.SymbolConfigs[update.S]
	lev := 1.0
	if ok {
		lev = existing.Leverage
	}

	s.state.UpdateSymbolConfig(SymbolConfig{
		Symbol:     update.S,
		Leverage:   lev,
		MarginMode: mode,
	})
}

// handlePositions processes position snapshot/updates.
//
// Pacifica format (array):
//
//	[{
//	  "s": "BTC",           // symbol
//	  "d": "bid",           // side (bid = long, ask = short)
//	  "a": "0.00022",       // position amount
//	  "p": "87185",         // average entry price
//	  "m": "0",             // position margin
//	  "f": "-0.00023989",   // position funding fee
//	  "i": false,           // isolated mode
//	  "l": null,            // liquidation price (null if N/A)
//	  "t": 1764133203991    // timestamp ms
//	}]
func (s *Subscriber) handlePositions(data json.RawMessage) {
	var positions []struct {
		S string      `json:"s"` // symbol
		D string      `json:"d"` // side: "bid" or "ask"
		A string      `json:"a"` // amount
		P string      `json:"p"` // entry price
		M string      `json:"m"` // margin
		F string      `json:"f"` // funding fee
		I bool        `json:"i"` // isolated
		L *string     `json:"l"` // liquidation price (nullable)
	}
	if err := json.Unmarshal(data, &positions); err != nil {
		s.logger.Warn("pacifica: parse account_positions", "err", err)
		return
	}

	var parsed []AccountPosition
	for _, p := range positions {
		amount := parseFloat(p.A)
		if amount == 0 {
			continue
		}

		// Pacifica uses "bid" for long, "ask" for short
		side := "long"
		if p.D == "ask" {
			side = "short"
		}

		var liqPrice float64
		if p.L != nil {
			liqPrice = parseFloat(*p.L)
		}

		parsed = append(parsed, AccountPosition{
			Symbol:        p.S,
			Side:          side,
			Size:          amount,
			EntryPrice:    parseFloat(p.P),
			MarkPrice:     0, // not in position updates — comes from market data
			UnrealizedPnL: 0, // not directly in this message
			Leverage:      0, // comes from account_leverage channel
			MarginUsed:    parseFloat(p.M),
			LiqPrice:      liqPrice,
		})
	}

	s.state.UpdatePositions(parsed)
}

// handleLeverage processes per-symbol leverage updates.
//
// Pacifica format (single object, not array):
//
//	{
//	  "u": "42trU9A5...",  // account
//	  "s": "BTC",          // symbol
//	  "l": "12",           // leverage as string
//	  "t": 1234567890      // timestamp ms
//	}
func (s *Subscriber) handleLeverage(data json.RawMessage) {
	var update struct {
		S string `json:"s"` // symbol
		L string `json:"l"` // leverage (string)
	}
	if err := json.Unmarshal(data, &update); err != nil {
		s.logger.Warn("pacifica: parse account_leverage", "err", err)
		return
	}

	lev := parseFloat(update.L)
	if lev <= 0 {
		lev = 1
	}

	// Preserve existing margin mode
	snap := s.state.Snapshot()
	mode := MarginModeUnknown
	if existing, ok := snap.SymbolConfigs[update.S]; ok {
		mode = existing.MarginMode
	}

	s.state.UpdateSymbolConfig(SymbolConfig{
		Symbol:     update.S,
		Leverage:   lev,
		MarginMode: mode,
	})
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
