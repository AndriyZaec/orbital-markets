package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	restURL   = "https://api.hyperliquid.xyz/info"
	wsURL     = "wss://api.hyperliquid.xyz/ws"
	venueName = "hyperliquid"
)

// REST response types

type metaResponse struct {
	Universe []metaAsset `json:"universe"`
}

type metaAsset struct {
	Name string `json:"name"`
}

type assetCtx struct {
	MarkPx       string `json:"markPx"`
	OraclePx     string `json:"oraclePx"`
	Funding      string `json:"funding"`
	OpenInterest string `json:"openInterest"`
}

// WS types

type wsSubscription struct {
	Method       string `json:"method"`
	Subscription any    `json:"subscription"`
}

type bboSub struct {
	Type string `json:"type"`
	Coin string `json:"coin"`
}

type wsMessage struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

type bboData struct {
	Coin string `json:"coin"`
	Time int64  `json:"time"`
	Bid  string `json:"bid"`
	Ask  string `json:"ask"`
}

// Internal state per asset
type assetState struct {
	markPrice    float64
	indexPrice   float64
	fundingRate  float64
	openInterest float64
	bidPrice     float64
	askPrice     float64
	timestamp    time.Time
}

type Adapter struct {
	mu     sync.RWMutex
	assets map[string]*assetState
	logger *slog.Logger
	client *http.Client
}

func New(logger *slog.Logger) *Adapter {
	return &Adapter{
		assets: make(map[string]*assetState),
		logger: logger,
		client: &http.Client{Timeout: 10 * time.Second},
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
		ts := s.timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		out = append(out, venue.MarketData{
			Venue:        venueName,
			Asset:        name,
			MarketKey:    name,
			MarkPrice:    s.markPrice,
			IndexPrice:   s.indexPrice,
			FundingRate:  s.fundingRate,
			BidPrice:     s.bidPrice,
			AskPrice:     s.askPrice,
			OpenInterest: s.openInterest,
			Timestamp:    ts,
		})
	}
	return out, nil
}

// Run starts both the REST poller and WS listener. Call as a goroutine.
func (a *Adapter) Run(ctx context.Context) {
	// Initial REST fetch to discover assets
	a.pollREST(ctx)

	go a.restLoop(ctx)
	go a.wsLoop(ctx)

	<-ctx.Done()
}

func (a *Adapter) restLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pollREST(ctx)
		}
	}
}

func (a *Adapter) pollREST(ctx context.Context) {
	body := `{"type":"metaAndAssetCtxs"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, restURL, strings.NewReader(body))
	if err != nil {
		a.logger.Error("hyperliquid rest: build request", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		a.logger.Error("hyperliquid rest: fetch", "err", err)
		return
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		a.logger.Error("hyperliquid rest: read body", "err", err)
		return
	}

	// Response is a tuple: [meta, [assetCtx, ...]]
	var tuple []json.RawMessage
	if err := json.Unmarshal(raw, &tuple); err != nil || len(tuple) < 2 {
		a.logger.Error("hyperliquid rest: parse tuple", "err", err)
		return
	}

	var meta metaResponse
	if err := json.Unmarshal(tuple[0], &meta); err != nil {
		a.logger.Error("hyperliquid rest: parse meta", "err", err)
		return
	}

	var ctxs []assetCtx
	if err := json.Unmarshal(tuple[1], &ctxs); err != nil {
		a.logger.Error("hyperliquid rest: parse asset ctxs", "err", err)
		return
	}

	if len(meta.Universe) != len(ctxs) {
		a.logger.Warn("hyperliquid rest: universe/ctx length mismatch",
			"universe", len(meta.Universe), "ctxs", len(ctxs))
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	n := min(len(meta.Universe), len(ctxs))
	for i := 0; i < n; i++ {
		name := meta.Universe[i].Name
		c := ctxs[i]

		state, exists := a.assets[name]
		if !exists {
			state = &assetState{}
			a.assets[name] = state
		}

		state.markPrice = parseFloat(c.MarkPx)
		state.indexPrice = parseFloat(c.OraclePx)
		state.fundingRate = parseFloat(c.Funding)
		state.openInterest = parseFloat(c.OpenInterest)
	}
}

func (a *Adapter) wsLoop(ctx context.Context) {
	for {
		err := a.connectWS(ctx)
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("hyperliquid ws disconnected, reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *Adapter) connectWS(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Subscribe to BBO for all known assets
	a.mu.RLock()
	coins := make([]string, 0, len(a.assets))
	for name := range a.assets {
		coins = append(coins, name)
	}
	a.mu.RUnlock()

	for _, coin := range coins {
		sub := wsSubscription{
			Method: "subscribe",
			Subscription: bboSub{
				Type: "bbo",
				Coin: coin,
			},
		}
		if err := conn.WriteJSON(sub); err != nil {
			return fmt.Errorf("subscribe %s: %w", coin, err)
		}
	}

	a.logger.Info("hyperliquid ws connected", "subscriptions", len(coins))

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

		if msg.Channel != "bbo" {
			continue
		}

		var bbo bboData
		if err := json.Unmarshal(msg.Data, &bbo); err != nil {
			a.logger.Warn("hyperliquid ws: parse bbo", "err", err)
			continue
		}

		a.mu.Lock()
		if state, ok := a.assets[bbo.Coin]; ok {
			state.bidPrice = parseFloat(bbo.Bid)
			state.askPrice = parseFloat(bbo.Ask)
			if bbo.Time > 0 {
				state.timestamp = time.UnixMilli(bbo.Time)
			}
		}
		a.mu.Unlock()
	}
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
