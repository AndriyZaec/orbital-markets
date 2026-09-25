package pacifica

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	wsURL             = "wss://ws.pacifica.fi/ws"
	infoURL           = "https://api.pacifica.fi/api/v1/info"
	fundingHistoryURL = "https://api.pacifica.fi/api/v1/funding/history"
	venueName         = "pacifica"
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
	markPrice          float64
	indexPrice         float64
	fundingRate        float64
	openInterest       float64
	bidPrice           float64
	bidSize            float64 // notional
	askPrice           float64
	askSize            float64 // notional
	maxLeverage        int
	lotSize            string
	timestamp          time.Time
	markHealth         venue.ComponentHealth
	indexHealth        venue.ComponentHealth
	fundingHealth      venue.ComponentHealth
	openInterestHealth venue.ComponentHealth
	bookHealth         venue.ComponentHealth
}

type Adapter struct {
	mu               sync.RWMutex
	assets           map[string]*assetState
	logger           *slog.Logger
	client           *http.Client
	metadataMu       sync.Mutex
	fundingURL       string
	now              func() time.Time
	streamGeneration uint64
	streamConnected  bool
	streamReceivedAt time.Time
}

func New(logger *slog.Logger) *Adapter {
	return &Adapter{
		assets:     make(map[string]*assetState),
		logger:     logger,
		client:     &http.Client{Timeout: 10 * time.Second},
		fundingURL: fundingHistoryURL,
		now:        time.Now,
	}
}

func (a *Adapter) FundingPayments(ctx context.Context, account, asset string, since, until time.Time) ([]venue.FundingPayment, error) {
	cursor := ""
	var payments []venue.FundingPayment
	for page := 0; page < 100; page++ {
		query := url.Values{"account": {account}, "limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.fundingURL+"?"+query.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("build Pacifica funding history request: %w", err)
		}
		response, err := a.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("fetch Pacifica funding history: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Pacifica funding history: %w", readErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("Pacifica funding history returned HTTP %d", response.StatusCode)
		}
		var result struct {
			Success bool `json:"success"`
			Data    []struct {
				HistoryID int64  `json:"history_id"`
				Symbol    string `json:"symbol"`
				Payout    string `json:"payout"`
				CreatedAt int64  `json:"created_at"`
			} `json:"data"`
			NextCursor string `json:"next_cursor"`
			HasMore    bool   `json:"has_more"`
		}
		if err := json.Unmarshal(body, &result); err != nil || !result.Success {
			return nil, fmt.Errorf("decode Pacifica funding history")
		}
		reachedStart := false
		for _, item := range result.Data {
			paidAt := time.UnixMilli(item.CreatedAt).UTC()
			if paidAt.Before(since) {
				reachedStart = true
				continue
			}
			if paidAt.After(until) || !strings.EqualFold(item.Symbol, asset) {
				continue
			}
			amount, err := strconv.ParseFloat(item.Payout, 64)
			if err != nil {
				return nil, fmt.Errorf("parse Pacifica funding payout: %w", err)
			}
			payments = append(payments, venue.FundingPayment{
				ExternalID: strconv.FormatInt(item.HistoryID, 10), Venue: venueName, Account: account,
				Asset: item.Symbol, AmountUSD: amount, PaidAt: paidAt,
			})
		}
		if !result.HasMore || result.NextCursor == "" || reachedStart {
			return payments, nil
		}
		cursor = result.NextCursor
	}
	return nil, fmt.Errorf("Pacifica funding history pagination limit exceeded")
}

func (a *Adapter) Name() string {
	return venueName
}

func (a *Adapter) FetchMarketData(ctx context.Context) ([]venue.MarketData, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]venue.MarketData, 0, len(a.assets))
	for name, s := range a.assets {
		streamSource := venue.SourceHealth{Status: venue.HealthUnavailable, Reason: venue.HealthReasonNotObserved}
		if a.streamGeneration > 0 {
			streamSource = venue.SourceHealth{
				Status: venue.HealthUnavailable, Generation: a.streamGeneration,
				ReceivedAt: a.streamReceivedAt, Reason: venue.HealthReasonDisconnected,
			}
			if a.streamConnected {
				streamSource.Status = venue.HealthAvailable
				streamSource.Reason = ""
			}
		}
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
			MaxLeverage:  s.maxLeverage,
			Timestamp:    s.timestamp,
			Health: &venue.MarketHealth{
				Mark: pacificaComponentHealth(s.markHealth), Index: pacificaComponentHealth(s.indexHealth),
				Funding: pacificaComponentHealth(s.fundingHealth), Book: pacificaComponentHealth(s.bookHealth),
				OpenInterest: pacificaComponentHealth(s.openInterestHealth),
				REST:         venue.SourceHealth{Status: venue.HealthUnavailable, Reason: venue.HealthReasonNotObserved},
				Stream:       streamSource,
			},
		})
	}
	return out, nil
}

// RefreshMetadata loads current per-symbol execution constraints from REST.
func (a *Adapter) RefreshMetadata(ctx context.Context) error {
	a.metadataMu.Lock()
	defer a.metadataMu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, infoURL, nil)
	if err != nil {
		return fmt.Errorf("build symbol info request: %w", err)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch symbol info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch symbol info: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Success bool `json:"success"`
		Data    []struct {
			Symbol      string `json:"symbol"`
			LotSize     string `json:"lot_size"`
			MaxLeverage int    `json:"max_leverage"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("parse symbol info: %w", err)
	}
	if !result.Success || len(result.Data) == 0 {
		return fmt.Errorf("symbol info response contains no data")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range result.Data {
		if s.MaxLeverage <= 0 {
			continue
		}
		state, exists := a.assets[s.Symbol]
		if !exists {
			state = &assetState{}
			a.assets[s.Symbol] = state
		}
		state.maxLeverage = s.MaxLeverage
		state.lotSize = s.LotSize
	}
	a.logger.Info("pacifica: symbol info loaded", "symbols", len(result.Data))
	return nil
}

func (a *Adapter) LotSize(symbol string) (string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	state, ok := a.assets[symbol]
	return state.lotSize, ok && state.lotSize != ""
}

// Connect starts the WebSocket connection and processes messages until ctx is cancelled.
func (a *Adapter) Connect(ctx context.Context) error {
	if err := a.RefreshMetadata(ctx); err != nil {
		a.logger.Warn("pacifica: initial symbol info", "err", err)
	}
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
	generation := a.markStreamConnected()
	defer a.markStreamDisconnected(generation)

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
			a.updatePrices(generation, prices)

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
			a.updateBBO(generation, bbo)
		}
	}
}

func (a *Adapter) markStreamConnected() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streamGeneration++
	a.streamConnected = true
	a.streamReceivedAt = time.Time{}
	return a.streamGeneration
}

func (a *Adapter) markStreamDisconnected(generation uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.streamGeneration == generation {
		a.streamConnected = false
	}
}

func (a *Adapter) updatePrices(generation uint64, prices []wsPrice) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.streamConnected || a.streamGeneration != generation {
		return
	}
	receivedAt := a.now()
	a.streamReceivedAt = receivedAt

	for _, p := range prices {
		sourceTime := time.UnixMilli(p.Timestamp)
		mark, markHealth := parsePacificaComponent(p.Mark, sourceTime, receivedAt, generation)
		oracle, indexHealth := parsePacificaComponent(p.Oracle, sourceTime, receivedAt, generation)
		funding, fundingHealth := parsePacificaComponent(p.Funding, sourceTime, receivedAt, generation)
		oi, openInterestHealth := parsePacificaComponent(p.OpenInterest, sourceTime, receivedAt, generation)
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
		state.timestamp = sourceTime
		state.markHealth = markHealth
		state.indexHealth = indexHealth
		state.fundingHealth = fundingHealth
		state.openInterestHealth = openInterestHealth

		// Use mid as fallback bid/ask until BBO arrives
		if state.bidPrice == 0 {
			state.bidPrice = mid
		}
		if state.askPrice == 0 {
			state.askPrice = mid
		}
	}
}

func (a *Adapter) updateBBO(generation uint64, bbo wsBBO) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.streamConnected || a.streamGeneration != generation {
		return
	}

	state, exists := a.assets[bbo.Symbol]
	if !exists {
		return
	}

	bidPx, bidPxErr := strconv.ParseFloat(bbo.BidPrice, 64)
	bidAmt, bidAmtErr := strconv.ParseFloat(bbo.BidAmount, 64)
	askPx, askPxErr := strconv.ParseFloat(bbo.AskPrice, 64)
	askAmt, askAmtErr := strconv.ParseFloat(bbo.AskAmount, 64)

	state.bidPrice = bidPx
	state.bidSize = bidAmt * bidPx // token amount × price = notional
	state.askPrice = askPx
	state.askSize = askAmt * askPx
	receivedAt := a.now()
	a.streamReceivedAt = receivedAt
	state.bookHealth = venue.ComponentHealth{
		Status: venue.HealthAvailable, ReceivedAt: receivedAt, SourceGeneration: generation,
	}
	if invalidPacificaFloat(bidPx, bidPxErr) || invalidPacificaFloat(bidAmt, bidAmtErr) ||
		invalidPacificaFloat(askPx, askPxErr) || invalidPacificaFloat(askAmt, askAmtErr) {
		state.bookHealth.Status = venue.HealthUnavailable
		state.bookHealth.Reason = venue.HealthReasonInvalidValue
	}

	if bbo.Timestamp > 0 {
		state.timestamp = time.UnixMilli(bbo.Timestamp)
		state.bookHealth.SourceTime = state.timestamp
	}
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parsePacificaComponent(
	raw string,
	sourceTime time.Time,
	receivedAt time.Time,
	generation uint64,
) (float64, venue.ComponentHealth) {
	value, err := strconv.ParseFloat(raw, 64)
	health := venue.ComponentHealth{
		Status: venue.HealthAvailable, SourceTime: sourceTime,
		ReceivedAt: receivedAt, SourceGeneration: generation,
	}
	if invalidPacificaFloat(value, err) {
		health.Status = venue.HealthUnavailable
		health.Reason = venue.HealthReasonInvalidValue
	}
	return value, health
}

func invalidPacificaFloat(value float64, err error) bool {
	return err != nil || math.IsNaN(value) || math.IsInf(value, 0)
}

func pacificaComponentHealth(health venue.ComponentHealth) venue.ComponentHealth {
	if health.Status == "" {
		return venue.ComponentHealth{Status: venue.HealthUnavailable, Reason: venue.HealthReasonNotObserved}
	}
	return health
}
