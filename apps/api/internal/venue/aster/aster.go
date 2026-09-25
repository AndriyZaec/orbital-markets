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
	venueName               = "aster"
	defaultRESTURL          = "https://fapi.asterdex.com"
	defaultWSURL            = "wss://fstream.asterdex.com/stream?streams=!markPrice@arr@1s/!bookTicker"
	fullRESTRefreshPeriod   = 15 * time.Minute
	websocketReconnectWait  = 5 * time.Second
	maxResponseBytes        = 8 << 20
	maxSnapshotAge          = 30 * time.Second
	maxClockSkew            = 5 * time.Second
	openInterestWorkers     = 8
	maxPendingStreamSymbols = 2048
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
	EventType            string `json:"e"`
	EventTime            int64  `json:"E"`
	Symbol               string `json:"s"`
	MarkPrice            string `json:"p"`
	EstimatedSettlePrice string `json:"P"`
	IndexPrice           string `json:"i"`
	FundingRate          string `json:"r"`
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

type metadataSnapshot struct {
	active        map[string]marketMetadata
	inactive      map[string]struct{}
	exchangeCount int
	fundingCount  int
}

type restMarketSnapshot struct {
	markets      map[string]marketState
	markCount    int
	indexCount   int
	fundingCount int
	bookCount    int
}

type refreshToken struct {
	expectedGeneration   uint64
	expectedSynchronized bool
	bookGeneration       uint64
	synchronize          bool
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
	indexUpdatedAt    time.Time
	fundingUpdatedAt  time.Time
	bookUpdatedAt     time.Time
	bookUpdateID      int64
	bookGeneration    uint64
	openInterest      float64
	openInterestKnown bool
}

// Adapter combines an atomic Futures V3 REST bootstrap with all-market
// WebSocket updates. Execution wiring is intentionally separate.
type Adapter struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	markets   map[string]marketState
	logger    *slog.Logger
	client    *http.Client
	restURL   string
	wsURL     string
	wsDialer  *websocket.Dialer
	now       func() time.Time

	streamGeneration     uint64
	streamConnected      bool
	streamSynchronized   bool
	lastStreamReceivedAt time.Time
	unknownStreamSymbols map[string]struct{}
	pendingMarks         map[string]marketState
	pendingBooks         map[string]marketState
}

func New(logger *slog.Logger) *Adapter {
	return &Adapter{
		markets:              make(map[string]marketState),
		logger:               logger,
		client:               &http.Client{Timeout: 10 * time.Second},
		restURL:              defaultRESTURL,
		wsURL:                defaultWSURL,
		wsDialer:             websocket.DefaultDialer,
		now:                  time.Now,
		unknownStreamSymbols: make(map[string]struct{}),
		pendingMarks:         make(map[string]marketState),
		pendingBooks:         make(map[string]marketState),
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
	now := a.now()
	streamHealthy := a.streamConnected && a.streamSynchronized && snapshotTimeFresh(a.lastStreamReceivedAt, now)
	for _, symbol := range symbols {
		state := a.markets[symbol]
		bookFreshAt := state.bookUpdatedAt
		bookFresh := snapshotTimeFresh(state.bookUpdatedAt, now)
		if a.streamGeneration > 0 {
			bookFresh = streamHealthy && state.bookGeneration == a.streamGeneration
			bookFreshAt = a.lastStreamReceivedAt
		}
		if state.fundingIntervalHours <= 0 ||
			!snapshotTimeFresh(state.markUpdatedAt, now) ||
			!snapshotTimeFresh(state.indexUpdatedAt, now) ||
			!snapshotTimeFresh(state.fundingUpdatedAt, now) ||
			!bookFresh {
			continue
		}
		out = append(out, venue.MarketData{
			Venue: venueName, Asset: state.asset, MarketKey: symbol,
			MarkPrice: state.markPrice, IndexPrice: state.indexPrice,
			FundingRate: state.nativeFundingRate / float64(state.fundingIntervalHours),
			BidPrice:    state.bidPrice, BidSize: state.bidSize,
			AskPrice: state.askPrice, AskSize: state.askSize,
			OpenInterest: state.openInterest,
			Timestamp: olderTime(
				olderTime(state.markUpdatedAt, state.indexUpdatedAt),
				olderTime(state.fundingUpdatedAt, bookFreshAt),
			),
		})
	}
	return out, nil
}

// Refresh reconciles metadata and prices without treating a partial response as
// an authoritative delisting. It is the bootstrap and operator fallback.
func (a *Adapter) Refresh(ctx context.Context) error {
	a.mu.RLock()
	token := refreshToken{
		expectedGeneration:   a.streamGeneration,
		expectedSynchronized: a.streamSynchronized,
		bookGeneration:       a.streamGeneration,
	}
	if !token.expectedSynchronized {
		token.bookGeneration = 0
	}
	a.mu.RUnlock()
	return a.refresh(ctx, token)
}

func (a *Adapter) refresh(ctx context.Context, token refreshToken) error {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if a.refreshSuperseded(token) {
		return nil
	}

	metadata, err := a.fetchMetadata(ctx)
	if err != nil {
		return err
	}
	restMarkets, err := a.fetchRESTMarkets(ctx, metadata.active)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if a.streamGeneration != token.expectedGeneration ||
		a.streamSynchronized != token.expectedSynchronized ||
		(token.synchronize && !a.streamConnected) {
		a.mu.Unlock()
		a.logger.Info("aster REST market data reconciliation superseded",
			"expected_generation", token.expectedGeneration,
			"current_generation", a.streamGeneration,
		)
		return nil
	}
	previousCount := len(a.markets)
	markets := make(map[string]marketState, len(a.markets)+len(metadata.active))
	removed := 0
	metadataOmitted := 0
	for symbol, previous := range a.markets {
		if _, inactive := metadata.inactive[symbol]; inactive {
			removed++
			continue
		}
		if _, confirmed := metadata.active[symbol]; !confirmed {
			metadataOmitted++
		}
		markets[symbol] = previous
	}
	preserved := 0
	for symbol, currentMetadata := range metadata.active {
		state := restMarkets.markets[symbol]
		previous, existed := markets[symbol]
		state.marketMetadata = currentMetadata
		state.bookGeneration = token.bookGeneration
		if existed {
			if state.fundingIntervalHours == 0 {
				state.fundingIntervalHours = previous.fundingIntervalHours
			}
			if state.markUpdatedAt.IsZero() || previous.markUpdatedAt.After(state.markUpdatedAt) {
				state.markPrice = previous.markPrice
				state.markUpdatedAt = previous.markUpdatedAt
				preserved++
			}
			if state.indexUpdatedAt.IsZero() || previous.indexUpdatedAt.After(state.indexUpdatedAt) {
				state.indexPrice = previous.indexPrice
				state.indexUpdatedAt = previous.indexUpdatedAt
				preserved++
			}
			if state.fundingUpdatedAt.IsZero() || previous.fundingUpdatedAt.After(state.fundingUpdatedAt) {
				state.nativeFundingRate = previous.nativeFundingRate
				state.fundingUpdatedAt = previous.fundingUpdatedAt
				preserved++
			}
			if state.bookUpdatedAt.IsZero() || previous.bookUpdateID > state.bookUpdateID ||
				(previous.bookUpdateID == state.bookUpdateID && previous.bookUpdatedAt.After(state.bookUpdatedAt)) {
				state.bidPrice = previous.bidPrice
				state.bidSize = previous.bidSize
				state.askPrice = previous.askPrice
				state.askSize = previous.askSize
				state.bookUpdatedAt = previous.bookUpdatedAt
				state.bookUpdateID = previous.bookUpdateID
				state.bookGeneration = previous.bookGeneration
				preserved++
			}
			if !state.openInterestKnown && previous.openInterestKnown {
				state.openInterest = previous.openInterest
				state.openInterestKnown = true
			}
		}
		markets[symbol] = state
	}
	a.applyPendingStreamUpdates(markets)
	a.markets = markets
	if token.synchronize {
		a.streamSynchronized = true
		if a.lastStreamReceivedAt.IsZero() {
			a.lastStreamReceivedAt = a.now()
		}
	}
	a.mu.Unlock()
	a.logger.Info("aster REST market data reconciled",
		"previous_symbols", previousCount,
		"exchange_symbols", metadata.exchangeCount,
		"funding_symbols", metadata.fundingCount,
		"active_symbols", len(metadata.active),
		"mark_snapshots", restMarkets.markCount,
		"index_snapshots", restMarkets.indexCount,
		"funding_snapshots", restMarkets.fundingCount,
		"book_snapshots", restMarkets.bookCount,
		"preserved_components", preserved,
		"metadata_omitted_symbols", metadataOmitted,
		"removed_symbols", removed,
		"known_symbols", len(markets),
	)
	return nil
}

func (a *Adapter) refreshSuperseded(token refreshToken) bool {
	a.mu.RLock()
	superseded := a.streamGeneration != token.expectedGeneration ||
		a.streamSynchronized != token.expectedSynchronized ||
		(token.synchronize && !a.streamConnected)
	currentGeneration := a.streamGeneration
	a.mu.RUnlock()
	if superseded {
		a.logger.Info("aster REST market data reconciliation superseded",
			"expected_generation", token.expectedGeneration,
			"current_generation", currentGeneration,
		)
	}
	return superseded
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
	resyncStarted := time.Now()
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
	a.mu.Lock()
	a.streamGeneration++
	generation := a.streamGeneration
	a.streamConnected = true
	a.streamSynchronized = false
	a.lastStreamReceivedAt = time.Time{}
	a.unknownStreamSymbols = make(map[string]struct{})
	a.pendingMarks = make(map[string]marketState)
	a.pendingBooks = make(map[string]marketState)
	a.mu.Unlock()
	defer func() {
		a.disconnectStreamGeneration(generation)
	}()
	readErrors := make(chan error, 1)
	go func() {
		for {
			_, raw, err := connection.ReadMessage()
			if err != nil {
				a.disconnectStreamGeneration(generation)
				readErrors <- fmt.Errorf("read Aster market websocket: %w", err)
				return
			}
			if err := a.applyStreamMessage(raw); err != nil {
				a.logger.Warn("aster websocket message rejected", "err", err, "generation", generation)
			}
		}
	}()

	token := refreshToken{
		expectedGeneration: generation,
		bookGeneration:     generation,
		synchronize:        true,
	}
	if err := a.refresh(ctx, token); err != nil {
		return fmt.Errorf("resynchronize Aster market websocket generation %d: %w", generation, err)
	}
	a.mu.RLock()
	connected := a.streamGeneration == generation && a.streamConnected && a.streamSynchronized
	knownSymbols := len(a.markets)
	a.mu.RUnlock()
	if !connected {
		select {
		case err := <-readErrors:
			return err
		case <-ctx.Done():
			return ctx.Err()
		default:
			return fmt.Errorf("Aster market websocket generation %d was superseded during resynchronization", generation)
		}
	}
	a.logger.Info("aster market websocket health changed",
		"health", "healthy",
		"generation", generation,
		"known_symbols", knownSymbols,
		"resync_ms", time.Since(resyncStarted).Milliseconds(),
	)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-readErrors:
		return err
	}
}

func (a *Adapter) disconnectStreamGeneration(generation uint64) {
	a.mu.Lock()
	changed := false
	if a.streamGeneration == generation {
		changed = a.streamConnected || a.streamSynchronized
		a.streamConnected = false
		a.streamSynchronized = false
		a.lastStreamReceivedAt = time.Time{}
	}
	a.mu.Unlock()
	if changed {
		a.logger.Info("aster market websocket health changed",
			"health", "unavailable",
			"generation", generation,
		)
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
		a.observeStreamMessage()
		a.applyMarkPriceUpdates(updates)
	case "!bookTicker":
		var update bookTickerUpdate
		if err := json.Unmarshal(envelope.Data, &update); err != nil {
			return fmt.Errorf("decode book ticker update: %w", err)
		}
		a.observeStreamMessage()
		a.applyBookTickerUpdate(update)
	}
	return nil
}

func (a *Adapter) observeStreamMessage() {
	a.mu.Lock()
	if a.streamConnected {
		a.lastStreamReceivedAt = a.now()
	}
	a.mu.Unlock()
}

func (a *Adapter) applyMarkPriceUpdates(updates []markPriceUpdate) {
	for _, update := range updates {
		mark, markOK := positiveDecimal(update.MarkPrice)
		index, indexOK := positiveDecimal(update.IndexPrice)
		fundingRate, fundingOK := finiteDecimal(update.FundingRate)
		if (!markOK && !indexOK && !fundingOK) || update.EventTime <= 0 {
			continue
		}
		updatedAt := time.UnixMilli(update.EventTime).UTC()
		a.mu.Lock()
		state, ok := a.markets[update.Symbol]
		if ok {
			if markOK && updatedAt.After(state.markUpdatedAt) {
				state.markPrice = mark
				state.markUpdatedAt = updatedAt
			}
			if indexOK && updatedAt.After(state.indexUpdatedAt) {
				state.indexPrice = index
				state.indexUpdatedAt = updatedAt
			}
			if fundingOK && updatedAt.After(state.fundingUpdatedAt) {
				state.nativeFundingRate = fundingRate
				state.fundingUpdatedAt = updatedAt
			}
			a.markets[update.Symbol] = state
		} else if !ok && a.streamConnected &&
			(len(a.pendingMarks) < maxPendingStreamSymbols || hasMarketComponents(a.pendingMarks[update.Symbol])) {
			pending := a.pendingMarks[update.Symbol]
			if markOK && updatedAt.After(pending.markUpdatedAt) {
				pending.markPrice = mark
				pending.markUpdatedAt = updatedAt
			}
			if indexOK && updatedAt.After(pending.indexUpdatedAt) {
				pending.indexPrice = index
				pending.indexUpdatedAt = updatedAt
			}
			if fundingOK && updatedAt.After(pending.fundingUpdatedAt) {
				pending.nativeFundingRate = fundingRate
				pending.fundingUpdatedAt = updatedAt
			}
			a.pendingMarks[update.Symbol] = pending
		}
		a.mu.Unlock()
		if !ok {
			a.logUnknownStreamSymbol("mark", update.Symbol)
		}
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
		state.bookGeneration = a.streamGeneration
		a.markets[update.Symbol] = state
	} else if !ok && a.streamConnected &&
		(len(a.pendingBooks) < maxPendingStreamSymbols || !a.pendingBooks[update.Symbol].bookUpdatedAt.IsZero()) {
		pending := a.pendingBooks[update.Symbol]
		pendingNewer := update.UpdateID > pending.bookUpdateID ||
			(update.UpdateID == 0 && pending.bookUpdateID == 0 && updatedAt.After(pending.bookUpdatedAt))
		if pendingNewer {
			pending.bidPrice = bid
			pending.bidSize = bid * bidQty
			pending.askPrice = ask
			pending.askSize = ask * askQty
			pending.bookUpdatedAt = updatedAt
			pending.bookUpdateID = update.UpdateID
			pending.bookGeneration = a.streamGeneration
			a.pendingBooks[update.Symbol] = pending
		}
	}
	a.mu.Unlock()
	if !ok {
		a.logUnknownStreamSymbol("book", update.Symbol)
	}
}

// applyPendingStreamUpdates runs with a.mu held after metadata reconciliation.
func (a *Adapter) applyPendingStreamUpdates(markets map[string]marketState) {
	for symbol, pending := range a.pendingMarks {
		state, ok := markets[symbol]
		if !ok {
			continue
		}
		if pending.markUpdatedAt.After(state.markUpdatedAt) {
			state.markPrice = pending.markPrice
			state.markUpdatedAt = pending.markUpdatedAt
		}
		if pending.indexUpdatedAt.After(state.indexUpdatedAt) {
			state.indexPrice = pending.indexPrice
			state.indexUpdatedAt = pending.indexUpdatedAt
		}
		if pending.fundingUpdatedAt.After(state.fundingUpdatedAt) {
			state.nativeFundingRate = pending.nativeFundingRate
			state.fundingUpdatedAt = pending.fundingUpdatedAt
		}
		markets[symbol] = state
		delete(a.pendingMarks, symbol)
	}
	for symbol, pending := range a.pendingBooks {
		state, ok := markets[symbol]
		if !ok {
			continue
		}
		newer := pending.bookUpdateID > state.bookUpdateID ||
			(pending.bookUpdateID == 0 && state.bookUpdateID == 0 && pending.bookUpdatedAt.After(state.bookUpdatedAt))
		if newer {
			state.bidPrice = pending.bidPrice
			state.bidSize = pending.bidSize
			state.askPrice = pending.askPrice
			state.askSize = pending.askSize
			state.bookUpdatedAt = pending.bookUpdatedAt
			state.bookUpdateID = pending.bookUpdateID
			state.bookGeneration = pending.bookGeneration
			markets[symbol] = state
		}
		delete(a.pendingBooks, symbol)
	}
}

func hasMarketComponents(state marketState) bool {
	return !state.markUpdatedAt.IsZero() || !state.indexUpdatedAt.IsZero() || !state.fundingUpdatedAt.IsZero()
}

func (a *Adapter) logUnknownStreamSymbol(component, symbol string) {
	a.mu.Lock()
	if a.unknownStreamSymbols == nil {
		a.unknownStreamSymbols = make(map[string]struct{})
	}
	_, logged := a.unknownStreamSymbols[symbol]
	shouldLog := !logged && len(a.unknownStreamSymbols) < 32
	if shouldLog {
		a.unknownStreamSymbols[symbol] = struct{}{}
	}
	generation := a.streamGeneration
	a.mu.Unlock()
	if shouldLog {
		a.logger.Warn("aster stream update for unknown symbol",
			"component", component,
			"symbol", symbol,
			"generation", generation,
		)
	}
}

func (a *Adapter) fetchMetadata(ctx context.Context) (metadataSnapshot, error) {
	var exchange exchangeInfoResponse
	var funding []fundingInfo
	if err := a.getJSON(ctx, "/fapi/v3/exchangeInfo", &exchange); err != nil {
		return metadataSnapshot{}, err
	}
	if err := a.getJSON(ctx, "/fapi/v3/fundingInfo", &funding); err != nil {
		return metadataSnapshot{}, err
	}
	intervals := make(map[string]int, len(funding))
	for _, item := range funding {
		if item.FundingIntervalHours > 0 {
			intervals[item.Symbol] = item.FundingIntervalHours
		}
	}
	metadata := make(map[string]marketMetadata)
	inactive := make(map[string]struct{})
	for _, symbol := range exchange.Symbols {
		interval := intervals[symbol.Symbol]
		rules := parseOrderRules(symbol.Filters)
		if symbol.Status == "TRADING" && symbol.ContractType == "PERPETUAL" && symbol.QuoteAsset == "USDT" {
			metadata[symbol.Symbol] = marketMetadata{
				asset: symbol.BaseAsset, fundingIntervalHours: interval, orderRules: rules,
			}
		} else if symbol.Status != "TRADING" && symbol.ContractType == "PERPETUAL" && symbol.QuoteAsset == "USDT" {
			inactive[symbol.Symbol] = struct{}{}
		}
	}
	if len(metadata) == 0 {
		return metadataSnapshot{}, fmt.Errorf("Aster metadata contains no active USDT perpetuals")
	}
	return metadataSnapshot{
		active: metadata, inactive: inactive,
		exchangeCount: len(exchange.Symbols), fundingCount: len(intervals),
	}, nil
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
) (restMarketSnapshot, error) {
	var premiums []premiumIndex
	var books []bookTicker
	if err := a.getJSON(ctx, "/fapi/v3/premiumIndex", &premiums); err != nil {
		return restMarketSnapshot{}, err
	}
	if err := a.getJSON(ctx, "/fapi/v3/ticker/bookTicker", &books); err != nil {
		return restMarketSnapshot{}, err
	}
	premiumBySymbol := make(map[string]premiumIndex, len(premiums))
	for _, item := range premiums {
		premiumBySymbol[item.Symbol] = item
	}
	bookBySymbol := make(map[string]bookTicker, len(books))
	for _, item := range books {
		bookBySymbol[item.Symbol] = item
	}

	markets := make(map[string]marketState, len(metadata))
	markCount := 0
	indexCount := 0
	fundingCount := 0
	bookCount := 0
	for symbol, item := range metadata {
		state := marketState{marketMetadata: item}
		premium, premiumOK := premiumBySymbol[symbol]
		book, bookOK := bookBySymbol[symbol]
		if premiumOK {
			mark, markOK := positiveDecimal(premium.MarkPrice)
			index, indexOK := positiveDecimal(premium.IndexPrice)
			fundingRate, fundingOK := finiteDecimal(premium.LastFundingRate)
			if premium.Time > 0 {
				updatedAt := time.UnixMilli(premium.Time).UTC()
				if markOK {
					state.markPrice = mark
					state.markUpdatedAt = updatedAt
					markCount++
				}
				if indexOK {
					state.indexPrice = index
					state.indexUpdatedAt = updatedAt
					indexCount++
				}
				if fundingOK {
					state.nativeFundingRate = fundingRate
					state.fundingUpdatedAt = updatedAt
					fundingCount++
				}
			}
		}
		if bookOK {
			bid, bidOK := positiveDecimal(book.BidPrice)
			bidQty, bidQtyOK := positiveDecimal(book.BidQty)
			ask, askOK := positiveDecimal(book.AskPrice)
			askQty, askQtyOK := positiveDecimal(book.AskQty)
			if bidOK && bidQtyOK && askOK && askQtyOK && ask >= bid && book.Time > 0 {
				state.bidPrice = bid
				state.bidSize = bid * bidQty
				state.askPrice = ask
				state.askSize = ask * askQty
				state.bookUpdatedAt = time.UnixMilli(book.Time).UTC()
				state.bookUpdateID = book.UpdateID
				bookCount++
			}
		}
		markets[symbol] = state
	}
	a.fetchOpenInterest(ctx, markets)
	return restMarketSnapshot{
		markets: markets, markCount: markCount, indexCount: indexCount,
		fundingCount: fundingCount, bookCount: bookCount,
	}, nil
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
