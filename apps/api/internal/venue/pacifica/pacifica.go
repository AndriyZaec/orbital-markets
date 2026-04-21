package pacifica

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	wsURL     = "wss://ws.pacifica.fi/ws"
	venueName = "pacifica"
)

// wsMessage wraps both prices and bbo channel messages.
type wsMessage struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

type wsPrice struct {
	Symbol       string `json:"symbol"`
	Mark         string `json:"mark"`
	Oracle       string `json:"oracle"`
	Mid          string `json:"mid"`
	Funding      string `json:"funding"`
	OpenInterest string `json:"open_interest"`
	Timestamp    int64  `json:"timestamp"`
}

type wsBBO struct {
	Symbol    string `json:"s"`
	BidPrice  string `json:"b"`
	BidAmount string `json:"B"`
	AskPrice  string `json:"a"`
	AskAmount string `json:"A"`
	Timestamp int64  `json:"t"`
}

// assetState holds combined prices + bbo data for one symbol.
type assetState struct {
	markPrice    float64
	indexPrice   float64
	fundingRate  float64
	openInterest float64
	bidPrice     float64
	bidSize      float64 // notional
	askPrice     float64
	askSize      float64 // notional
	timestamp    time.Time
}

type Adapter struct {
	mu     sync.RWMutex
	assets map[string]*assetState
	logger *slog.Logger
}

func New(logger *slog.Logger) *Adapter {
	return &Adapter{
		assets: make(map[string]*assetState),
		logger: logger,
	}
}

func (a *Adapter) Name() string {
	return venueName
}

func (a *Adapter) FetchMarketData(ctx context.Context) ([]venue.MarketData, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]venue.MarketData, 0, len(a.assets))
	for name, s := range a.assets {
		out = append(out, venue.MarketData{
			Venue:        venueName,
			Asset:        name,
			MarketKey:    name,
			MarkPrice:    s.markPrice,
			IndexPrice:   s.indexPrice,
			FundingRate:  s.fundingRate,
			BidPrice:     s.bidPrice,
			BidSize:      s.bidSize,
			AskPrice:     s.askPrice,
			AskSize:      s.askSize,
			OpenInterest: s.openInterest,
			Timestamp:    s.timestamp,
		})
	}
	return out, nil
}

// Connect starts the WebSocket connection and processes messages until ctx is cancelled.
func (a *Adapter) Connect(ctx context.Context) error {
	for {
		err := a.connectAndListen(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.logger.Error("pacifica ws disconnected, reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *Adapter) connectAndListen(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Subscribe to prices (all symbols, funding/mark/OI)
	if err := conn.WriteJSON(map[string]any{
		"method": "subscribe",
		"params": map[string]string{"source": "prices"},
	}); err != nil {
		return fmt.Errorf("subscribe prices: %w", err)
	}

	a.logger.Info("pacifica ws connected")

	// Track which symbols we've subscribed BBO for
	bboSubscribed := make(map[string]bool)

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

		switch msg.Channel {
		case "prices":
			var prices []wsPrice
			if len(msg.Data) == 0 || msg.Data[0] != '[' {
				continue
			}
			if err := json.Unmarshal(msg.Data, &prices); err != nil {
				a.logger.Warn("pacifica: parse prices", "err", err)
				continue
			}
			a.updatePrices(prices)

			// Subscribe to BBO for any new symbols we discovered
			for _, p := range prices {
				if bboSubscribed[p.Symbol] {
					continue
				}
				if err := conn.WriteJSON(map[string]any{
					"method": "subscribe",
					"params": map[string]string{
						"source": "bbo",
						"symbol": p.Symbol,
					},
				}); err != nil {
					a.logger.Warn("pacifica: subscribe bbo", "symbol", p.Symbol, "err", err)
					continue
				}
				bboSubscribed[p.Symbol] = true
			}

		case "bbo":
			var bbo wsBBO
			if err := json.Unmarshal(msg.Data, &bbo); err != nil {
				continue
			}
			a.updateBBO(bbo)
		}
	}
}

func (a *Adapter) updatePrices(prices []wsPrice) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, p := range prices {
		mark := parseFloat(p.Mark)
		oracle := parseFloat(p.Oracle)
		funding := parseFloat(p.Funding)
		oi := parseFloat(p.OpenInterest)
		mid := parseFloat(p.Mid)

		state, exists := a.assets[p.Symbol]
		if !exists {
			state = &assetState{}
			a.assets[p.Symbol] = state
		}

		state.markPrice = mark
		state.indexPrice = oracle
		state.fundingRate = funding
		state.openInterest = oi
		state.timestamp = time.UnixMilli(p.Timestamp)

		// Use mid as fallback bid/ask until BBO arrives
		if state.bidPrice == 0 {
			state.bidPrice = mid
		}
		if state.askPrice == 0 {
			state.askPrice = mid
		}
	}
}

func (a *Adapter) updateBBO(bbo wsBBO) {
	a.mu.Lock()
	defer a.mu.Unlock()

	state, exists := a.assets[bbo.Symbol]
	if !exists {
		return
	}

	bidPx := parseFloat(bbo.BidPrice)
	bidAmt := parseFloat(bbo.BidAmount)
	askPx := parseFloat(bbo.AskPrice)
	askAmt := parseFloat(bbo.AskAmount)

	state.bidPrice = bidPx
	state.bidSize = bidAmt * bidPx // token amount × price = notional
	state.askPrice = askPx
	state.askSize = askAmt * askPx

	if bbo.Timestamp > 0 {
		state.timestamp = time.UnixMilli(bbo.Timestamp)
	}
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
