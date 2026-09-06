package aster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	venueName              = "aster"
	defaultRESTURL         = "https://fapi.asterdex.com"
	defaultWSURL           = "wss://fstream.asterdex.com/stream?streams=!markPrice@arr@1s/!bookTicker"
	fullRESTRefreshPeriod  = 15 * time.Minute
	websocketReconnectWait = 5 * time.Second
	maxResponseBytes       = 8 << 20
	maxSnapshotAge         = 30 * time.Second
	maxClockSkew           = 5 * time.Second
	openInterestWorkers    = 8
)

type exchangeFilter struct {
	FilterType string `json:"filterType"`
	MinPrice   string `json:"minPrice"`
	MaxPrice   string `json:"maxPrice"`
	TickSize   string `json:"tickSize"`
	MinQty     string `json:"minQty"`
	MaxQty     string `json:"maxQty"`
	StepSize   string `json:"stepSize"`
	Notional   string `json:"notional"`
}

type exchangeInfoResponse struct {
	Symbols []struct {
		Symbol       string           `json:"symbol"`
		ContractType string           `json:"contractType"`
		Status       string           `json:"status"`
		BaseAsset    string           `json:"baseAsset"`
		QuoteAsset   string           `json:"quoteAsset"`
		Filters      []exchangeFilter `json:"filters"`
	} `json:"symbols"`
}

type premiumIndex struct {
	Symbol          string `json:"symbol"`
	MarkPrice       string `json:"markPrice"`
	IndexPrice      string `json:"indexPrice"`
	LastFundingRate string `json:"lastFundingRate"`
	Time            int64  `json:"time"`
}

type bookTicker struct {
	UpdateID int64  `json:"lastUpdateId"`
	Symbol   string `json:"symbol"`
	BidPrice string `json:"bidPrice"`
	BidQty   string `json:"bidQty"`
	AskPrice string `json:"askPrice"`
	AskQty   string `json:"askQty"`
	Time     int64  `json:"time"`
}

type fundingInfo struct {
	Symbol               string `json:"symbol"`
	FundingIntervalHours int    `json:"fundingIntervalHours"`
}

type openInterestResponse struct {
	Symbol       string `json:"symbol"`
	OpenInterest string `json:"openInterest"`
}

type streamEnvelope struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

type markPriceUpdate struct {
	EventType   string `json:"e"`
	EventTime   int64  `json:"E"`
	Symbol      string `json:"s"`
	MarkPrice   string `json:"p"`
	IndexPrice  string `json:"i"`
	FundingRate string `json:"r"`
}

type bookTickerUpdate struct {
	EventType       string `json:"e"`
	UpdateID        int64  `json:"u"`
	EventTime       int64  `json:"E"`
	TransactionTime int64  `json:"T"`
	Symbol          string `json:"s"`
	BidPrice        string `json:"b"`
	BidQty          string `json:"B"`
	AskPrice        string `json:"a"`
	AskQty          string `json:"A"`
}

type marketMetadata struct {
	asset                string
	fundingIntervalHours int
	orderRules           OrderRules
}

// OrderRules are the exchange filters consumed by Aster LIMIT IOC orders.
type OrderRules struct {
	MinPrice     string
	MaxPrice     string
	TickSize     string
	MinQuantity  string
	MaxQuantity  string
	QuantityStep string
	MinNotional  string
}

type marketState struct {
	marketMetadata
	markPrice         float64
	indexPrice        float64
	nativeFundingRate float64
	bidPrice          float64
	bidSize           float64
	askPrice          float64
	askSize           float64
	markUpdatedAt     time.Time
	bookUpdatedAt     time.Time
	bookUpdateID      int64
	openInterest      float64
	openInterestKnown bool
}

// Adapter combines an atomic Futures V3 REST bootstrap with all-market
// WebSocket updates. Execution wiring is intentionally separate.
type Adapter struct {
	mu       sync.RWMutex
	markets  map[string]marketState
	logger   *slog.Logger
	client   *http.Client
	restURL  string
	wsURL    string
	wsDialer *websocket.Dialer
}

func New(logger *slog.Logger) *Adapter {
	return &Adapter{
		markets:  make(map[string]marketState),
		logger:   logger,
		client:   &http.Client{Timeout: 10 * time.Second},
		restURL:  defaultRESTURL,
		wsURL:    defaultWSURL,
		wsDialer: websocket.DefaultDialer,
	}
}

func (a *Adapter) Name() string { return venueName }

func (a *Adapter) OrderRules(symbol string) (OrderRules, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	state, ok := a.markets[symbol]
	return state.orderRules, ok && state.orderRules.valid()
}

func (a *Adapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	symbols := make([]string, 0, len(a.markets))
	for symbol := range a.markets {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	out := make([]venue.MarketData, 0, len(symbols))
	now := time.Now()
	for _, symbol := range symbols {
		state := a.markets[symbol]
		if !snapshotTimeFresh(state.markUpdatedAt, now) || !snapshotTimeFresh(state.bookUpdatedAt, now) {
			continue
		}
		out = append(out, venue.MarketData{
			Venue: venueName, Asset: state.asset, MarketKey: symbol,
			MarkPrice: state.markPrice, IndexPrice: state.indexPrice,
			FundingRate: state.nativeFundingRate / float64(state.fundingIntervalHours),
			BidPrice:    state.bidPrice, BidSize: state.bidSize,
			AskPrice: state.askPrice, AskSize: state.askSize,
			OpenInterest: state.openInterest,
			Timestamp:    olderTime(state.markUpdatedAt, state.bookUpdatedAt),
		})
	}
	return out, nil
}

// Refresh atomically replaces metadata and prices from one complete REST
// snapshot. It is the bootstrap and an explicit operator fallback.
func (a *Adapter) Refresh(ctx context.Context) error {
	metadata, err := a.fetchMetadata(ctx)
	if err != nil {
		return err
	}
	markets, err := a.fetchRESTMarkets(ctx, metadata)
	if err != nil {
		return err
	}
	a.mu.Lock()
	for symbol, state := range markets {
		previous, ok := a.markets[symbol]
		if !ok {
			continue
		}
		if previous.markUpdatedAt.After(state.markUpdatedAt) {
			state.markPrice = previous.markPrice
			state.indexPrice = previous.indexPrice
			state.nativeFundingRate = previous.nativeFundingRate
			state.markUpdatedAt = previous.markUpdatedAt
		}
		if previous.bookUpdateID > state.bookUpdateID ||
			(previous.bookUpdateID == state.bookUpdateID && previous.bookUpdatedAt.After(state.bookUpdatedAt)) {
			state.bidPrice = previous.bidPrice
			state.bidSize = previous.bidSize
			state.askPrice = previous.askPrice
			state.askSize = previous.askSize
			state.bookUpdatedAt = previous.bookUpdatedAt
			state.bookUpdateID = previous.bookUpdateID
		}
		if !state.openInterestKnown && previous.openInterestKnown {
			state.openInterest = previous.openInterest
			state.openInterestKnown = true
		}
		markets[symbol] = state
	}
	a.markets = markets
	a.mu.Unlock()
	a.logger.Debug("aster REST market data updated", "symbols", len(markets))
	return nil
}

// Run bootstraps from REST, maintains metadata at low frequency, and reconnects
// the two all-market streams until the context is cancelled.
func (a *Adapter) Run(ctx context.Context) {
	for {
		if err := a.Refresh(ctx); err == nil {
			break
		} else {
			a.logger.Error("aster initial REST refresh", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(websocketReconnectWait):
		}
	}
	go a.restRefreshLoop(ctx)
	for {
		err := a.connectAndListen(ctx)
		if ctx.Err() != nil {
			return
		}
		a.logger.Error("aster websocket disconnected, reconnecting", "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(websocketReconnectWait):
		}
	}
}

func (a *Adapter) restRefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(fullRESTRefreshPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.Refresh(ctx); err != nil {
				a.logger.Error("aster periodic REST refresh", "err", err)
			}
		}
	}
}

func (a *Adapter) connectAndListen(ctx context.Context) error {
	connection, _, err := a.wsDialer.DialContext(ctx, a.wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial Aster market websocket: %w", err)
	}
	defer connection.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-done:
		}
	}()
	a.logger.Info("aster market websocket connected")

	for {
		_, raw, err := connection.ReadMessage()
		if err != nil {
			return fmt.Errorf("read Aster market websocket: %w", err)
		}
		if err := a.applyStreamMessage(raw); err != nil {
			a.logger.Warn("aster websocket message rejected", "err", err)
		}
	}
}

func (a *Adapter) applyStreamMessage(raw []byte) error {
	var envelope streamEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode stream envelope: %w", err)
	}
	switch envelope.Stream {
	case "!markPrice@arr@1s":
		var updates []markPriceUpdate
		if err := json.Unmarshal(envelope.Data, &updates); err != nil {
			return fmt.Errorf("decode mark price updates: %w", err)
		}
		a.applyMarkPriceUpdates(updates)
	case "!bookTicker":
		var update bookTickerUpdate
		if err := json.Unmarshal(envelope.Data, &update); err != nil {
			return fmt.Errorf("decode book ticker update: %w", err)
		}
		a.applyBookTickerUpdate(update)
	}
	return nil
}

func (a *Adapter) applyMarkPriceUpdates(updates []markPriceUpdate) {
	for _, update := range updates {
		mark, markOK := positiveDecimal(update.MarkPrice)
		index, indexOK := positiveDecimal(update.IndexPrice)
		fundingRate, fundingOK := finiteDecimal(update.FundingRate)
		if !markOK || !indexOK || !fundingOK || update.EventTime <= 0 {
			continue
		}
		updatedAt := time.UnixMilli(update.EventTime).UTC()
		a.mu.Lock()
		state, ok := a.markets[update.Symbol]
		if ok && updatedAt.After(state.markUpdatedAt) {
			state.markPrice = mark
			state.indexPrice = index
			state.nativeFundingRate = fundingRate
			state.markUpdatedAt = updatedAt
			a.markets[update.Symbol] = state
		}
		a.mu.Unlock()
	}
}

func (a *Adapter) applyBookTickerUpdate(update bookTickerUpdate) {
	bid, bidOK := positiveDecimal(update.BidPrice)
	bidQty, bidQtyOK := positiveDecimal(update.BidQty)
	ask, askOK := positiveDecimal(update.AskPrice)
	askQty, askQtyOK := positiveDecimal(update.AskQty)
	timestamp := update.TransactionTime
	if timestamp <= 0 {
		timestamp = update.EventTime
	}
	if !bidOK || !bidQtyOK || !askOK || !askQtyOK || ask < bid || timestamp <= 0 {
		return
	}
	updatedAt := time.UnixMilli(timestamp).UTC()
	a.mu.Lock()
	state, ok := a.markets[update.Symbol]
	newer := update.UpdateID > state.bookUpdateID ||
		(update.UpdateID == 0 && state.bookUpdateID == 0 && updatedAt.After(state.bookUpdatedAt))
	if ok && newer {
		state.bidPrice = bid
		state.bidSize = bid * bidQty
		state.askPrice = ask
		state.askSize = ask * askQty
		state.bookUpdatedAt = updatedAt
		state.bookUpdateID = update.UpdateID
		a.markets[update.Symbol] = state
	}
	a.mu.Unlock()
}

func (a *Adapter) fetchMetadata(ctx context.Context) (map[string]marketMetadata, error) {
	var exchange exchangeInfoResponse
	var funding []fundingInfo
	if err := a.getJSON(ctx, "/fapi/v3/exchangeInfo", &exchange); err != nil {
		return nil, err
	}
	if err := a.getJSON(ctx, "/fapi/v3/fundingInfo", &funding); err != nil {
		return nil, err
	}
	intervals := make(map[string]int, len(funding))
	for _, item := range funding {
		if item.FundingIntervalHours > 0 {
			intervals[item.Symbol] = item.FundingIntervalHours
		}
	}
	metadata := make(map[string]marketMetadata)
	for _, symbol := range exchange.Symbols {
		interval := intervals[symbol.Symbol]
		rules := parseOrderRules(symbol.Filters)
		if symbol.Status == "TRADING" && symbol.ContractType == "PERPETUAL" &&
			symbol.QuoteAsset == "USDT" && interval > 0 {
			metadata[symbol.Symbol] = marketMetadata{
				asset: symbol.BaseAsset, fundingIntervalHours: interval, orderRules: rules,
			}
		}
	}
	if len(metadata) == 0 {
		return nil, fmt.Errorf("Aster metadata contains no active USDT perpetuals with funding intervals")
	}
	return metadata, nil
}

func parseOrderRules(filters []exchangeFilter) OrderRules {
	var rules OrderRules
	for _, filter := range filters {
		switch filter.FilterType {
		case "PRICE_FILTER":
			rules.MinPrice = filter.MinPrice
			rules.MaxPrice = filter.MaxPrice
			rules.TickSize = filter.TickSize
		case "LOT_SIZE":
			rules.MinQuantity = filter.MinQty
			rules.MaxQuantity = filter.MaxQty
			rules.QuantityStep = filter.StepSize
		case "MIN_NOTIONAL":
			rules.MinNotional = filter.Notional
		}
	}
	return rules
}

func (r OrderRules) valid() bool {
	values := []string{
		r.MinPrice, r.MaxPrice, r.TickSize,
		r.MinQuantity, r.MaxQuantity, r.QuantityStep, r.MinNotional,
	}
	for _, value := range values {
		if _, ok := positiveDecimal(value); !ok {
			return false
		}
	}
	minPrice, _ := strconv.ParseFloat(r.MinPrice, 64)
	maxPrice, _ := strconv.ParseFloat(r.MaxPrice, 64)
	minQuantity, _ := strconv.ParseFloat(r.MinQuantity, 64)
	maxQuantity, _ := strconv.ParseFloat(r.MaxQuantity, 64)
	return minPrice <= maxPrice && minQuantity <= maxQuantity
}

func (a *Adapter) fetchRESTMarkets(
	ctx context.Context, metadata map[string]marketMetadata,
) (map[string]marketState, error) {
	var premiums []premiumIndex
	var books []bookTicker
	if err := a.getJSON(ctx, "/fapi/v3/premiumIndex", &premiums); err != nil {
		return nil, err
	}
	if err := a.getJSON(ctx, "/fapi/v3/ticker/bookTicker", &books); err != nil {
		return nil, err
	}
	premiumBySymbol := make(map[string]premiumIndex, len(premiums))
	for _, item := range premiums {
		premiumBySymbol[item.Symbol] = item
	}
	bookBySymbol := make(map[string]bookTicker, len(books))
	for _, item := range books {
		bookBySymbol[item.Symbol] = item
	}

	markets := make(map[string]marketState)
	for symbol, item := range metadata {
		premium, premiumOK := premiumBySymbol[symbol]
		book, bookOK := bookBySymbol[symbol]
		if !premiumOK || !bookOK {
			continue
		}
		mark, markOK := positiveDecimal(premium.MarkPrice)
		index, indexOK := positiveDecimal(premium.IndexPrice)
		fundingRate, fundingOK := finiteDecimal(premium.LastFundingRate)
		bid, bidOK := positiveDecimal(book.BidPrice)
		bidQty, bidQtyOK := positiveDecimal(book.BidQty)
		ask, askOK := positiveDecimal(book.AskPrice)
		askQty, askQtyOK := positiveDecimal(book.AskQty)
		if !markOK || !indexOK || !fundingOK || !bidOK || !bidQtyOK || !askOK || !askQtyOK || ask < bid ||
			premium.Time <= 0 || book.Time <= 0 {
			continue
		}
		markets[symbol] = marketState{
			marketMetadata: item,
			markPrice:      mark, indexPrice: index, nativeFundingRate: fundingRate,
			bidPrice: bid, bidSize: bid * bidQty, askPrice: ask, askSize: ask * askQty,
			markUpdatedAt: time.UnixMilli(premium.Time).UTC(),
			bookUpdatedAt: time.UnixMilli(book.Time).UTC(),
			bookUpdateID:  book.UpdateID,
		}
	}
	if len(markets) == 0 {
		return nil, fmt.Errorf("Aster market data contains no complete USDT perpetual snapshots")
	}
	a.fetchOpenInterest(ctx, markets)
	return markets, nil
}

func (a *Adapter) fetchOpenInterest(ctx context.Context, markets map[string]marketState) {
	type result struct {
		symbol string
		value  float64
		err    error
	}
	jobs := make(chan string)
	results := make(chan result)
	workers := min(openInterestWorkers, len(markets))
	symbols := make([]string, 0, len(markets))
	for symbol := range markets {
		symbols = append(symbols, symbol)
	}
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for symbol := range jobs {
				var response openInterestResponse
				err := a.getJSON(ctx, "/fapi/v3/openInterest?symbol="+url.QueryEscape(symbol), &response)
				value, valid := finiteDecimal(response.OpenInterest)
				if err == nil && (response.Symbol != symbol || !valid || value < 0) {
					err = fmt.Errorf("invalid Aster open interest for %s", symbol)
				}
				results <- result{symbol: symbol, value: value, err: err}
			}
		}()
	}
	go func() {
		for _, symbol := range symbols {
			jobs <- symbol
		}
		close(jobs)
		group.Wait()
		close(results)
	}()

	failures := 0
	for result := range results {
		if result.err != nil {
			failures++
			continue
		}
		state := markets[result.symbol]
		state.openInterest = result.value
		state.openInterestKnown = true
		markets[result.symbol] = state
	}
	if failures > 0 {
		a.logger.Warn("aster open interest refresh incomplete", "failed", failures, "symbols", len(markets))
	}
}

func (a *Adapter) getJSON(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.restURL+path, nil)
	if err != nil {
		return fmt.Errorf("build Aster %s request: %w", path, err)
	}
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("fetch Aster %s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Aster %s returned HTTP %d", path, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode Aster %s: %w", path, err)
	}
	return nil
}

func finiteDecimal(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func positiveDecimal(value string) (float64, bool) {
	parsed, ok := finiteDecimal(value)
	return parsed, ok && parsed > 0
}

func snapshotTimeFresh(timestamp, now time.Time) bool {
	return !timestamp.IsZero() && now.Sub(timestamp) <= maxSnapshotAge && !timestamp.After(now.Add(maxClockSkew))
}

func olderTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
