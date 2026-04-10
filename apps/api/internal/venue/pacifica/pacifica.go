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

// wsMessage is the raw message from the Pacifica WebSocket.
type wsMessage struct {
	Channel string    `json:"channel"`
	Data    []wsPrice `json:"data"`
}

type wsPrice struct {
	Symbol         string `json:"symbol"`
	Mark           string `json:"mark"`
	Oracle         string `json:"oracle"`
	Mid            string `json:"mid"`
	Funding        string `json:"funding"`
	NextFunding    string `json:"next_funding"`
	OpenInterest   string `json:"open_interest"`
	Volume24h      string `json:"volume_24h"`
	YesterdayPrice string `json:"yesterday_price"`
	Timestamp      int64  `json:"timestamp"`
}

// Adapter connects to Pacifica's WebSocket and keeps market data fresh.
type Adapter struct {
	mu        sync.RWMutex
	snapshots map[string]venue.MarketData
	logger    *slog.Logger
}

func New(logger *slog.Logger) *Adapter {
	return &Adapter{
		snapshots: make(map[string]venue.MarketData),
		logger:    logger,
	}
}

func (a *Adapter) Name() string {
	return venueName
}

func (a *Adapter) FetchMarketData(ctx context.Context) ([]venue.MarketData, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]venue.MarketData, 0, len(a.snapshots))
	for _, s := range a.snapshots {
		out = append(out, s)
	}
	return out, nil
}

// Connect starts the WebSocket connection and processes messages until ctx is cancelled.
// Should be called as a goroutine.
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

	sub := map[string]any{
		"method": "subscribe",
		"params": map[string]string{
			"source": "prices",
		},
	}
	if err := conn.WriteJSON(sub); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	a.logger.Info("pacifica ws connected")

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
			a.logger.Warn("pacifica ws parse error", "err", err)
			continue
		}

		if msg.Channel != "prices" {
			continue
		}

		a.updateSnapshots(msg.Data)
	}
}

func (a *Adapter) updateSnapshots(prices []wsPrice) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, p := range prices {
		md, err := toMarketData(p)
		if err != nil {
			a.logger.Warn("pacifica parse price", "symbol", p.Symbol, "err", err)
			continue
		}
		a.snapshots[p.Symbol] = md
	}
}

func toMarketData(p wsPrice) (venue.MarketData, error) {
	mark, err := strconv.ParseFloat(p.Mark, 64)
	if err != nil {
		return venue.MarketData{}, fmt.Errorf("mark: %w", err)
	}
	oracle, err := strconv.ParseFloat(p.Oracle, 64)
	if err != nil {
		return venue.MarketData{}, fmt.Errorf("oracle: %w", err)
	}
	mid, err := strconv.ParseFloat(p.Mid, 64)
	if err != nil {
		return venue.MarketData{}, fmt.Errorf("mid: %w", err)
	}
	funding, err := strconv.ParseFloat(p.Funding, 64)
	if err != nil {
		return venue.MarketData{}, fmt.Errorf("funding: %w", err)
	}
	oi, err := strconv.ParseFloat(p.OpenInterest, 64)
	if err != nil {
		return venue.MarketData{}, fmt.Errorf("open_interest: %w", err)
	}

	return venue.MarketData{
		Venue:        venueName,
		Asset:        p.Symbol,
		MarketKey:    p.Symbol,
		MarkPrice:    mark,
		IndexPrice:   oracle,
		FundingRate:  funding,
		BidPrice:     mid,
		AskPrice:     mid,
		OpenInterest: oi,
		Timestamp:    time.UnixMilli(p.Timestamp),
	}, nil
}
