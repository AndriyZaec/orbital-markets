package phoenix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	DefaultWSURL           = "wss://perp-api.phoenix.trade/v1/ws"
	websocketReconnectWait = 5 * time.Second
	websocketReadTimeout   = 30 * time.Second
	maxWebSocketMessage    = 8 * 1_024 * 1_024
	exchangeVersion        = 1
)

var errExchangeSequenceGap = errors.New("Phoenix exchange sequence gap")

// LiveMarketStats is the latest normalized Phoenix market-stats update.
type LiveMarketStats struct {
	Symbol            string
	MarkPrice         float64
	MidPrice          float64
	IndexPrice        float64
	HourlyFundingRate float64 // Fraction per hour, matching venue.MarketData.
	NativeFundingRate float64 // Fraction per native funding interval, not percentage points.
	OpenInterest      float64
	SourceTime        time.Time
	ReceivedAt        time.Time
	Generation        uint64
}

// LiveBook is the latest executable-band Phoenix best bid and offer.
type LiveBook struct {
	Symbol     string
	BidPrice   float64
	BidSizeUSD float64
	AskPrice   float64
	AskSizeUSD float64
	ReceivedAt time.Time
	Generation uint64
}

// MarketStreamSnapshot is an immutable copy of Phoenix public market state.
type MarketStreamSnapshot struct {
	Metadata           ExchangeMetadata
	Stats              map[string]LiveMarketStats
	Books              map[string]LiveBook
	Connected          bool
	Synchronized       bool
	Generation         uint64
	Sequence           uint64
	MetadataGeneration uint64
	MetadataReceivedAt time.Time
	LastReceivedAt     time.Time
}

// MarketStream maintains sequence-consistent Phoenix metadata and market stats.
type MarketStream struct {
	client        *Client
	logger        *slog.Logger
	wsURL         string
	dialer        *websocket.Dialer
	now           func() time.Time
	reconnectWait time.Duration

	mu                   sync.RWMutex
	accounts             ExchangeAccounts
	markets              map[string]MarketMetadata
	stats                map[string]LiveMarketStats
	books                map[string]LiveBook
	updates              chan struct{}
	connected            bool
	synchronized         bool
	generation           uint64
	sequence             uint64
	metadataGeneration   uint64
	metadataReceivedAt   time.Time
	lastReceivedAt       time.Time
	haveExchangeSnapshot bool
}

type wsEnvelope struct {
	Channel string `json:"channel"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Code    any    `json:"code"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

type exchangeEnvelope struct {
	Channel        string             `json:"channel"`
	MessageType    string             `json:"messageType"`
	Version        int                `json:"version"`
	SequenceNumber *stringUint64      `json:"sequenceNumber"`
	Exchange       wsExchangeAccounts `json:"exchange"`
	Markets        []wsMarketConfig   `json:"markets"`
	Ops            []json.RawMessage  `json:"ops"`
}

type wsExchangeAccounts struct {
	CurrentAuthorities exchangeAuthorities `json:"currentAuthorities"`
	PerpAssetMap       string              `json:"perpAssetMap"`
	GlobalTraderIndex  []string            `json:"globalTraderIndex"`
	ActiveTraderBuffer []string            `json:"activeTraderBuffer"`
}

type wsMarketConfig struct {
	Symbol           string          `json:"symbol"`
	MarketStatus     string          `json:"marketStatus"`
	MarketPubkey     string          `json:"marketPubkey"`
	SplinePubkey     string          `json:"splinePubkey"`
	TickSize         int64           `json:"tickSize"`
	BaseLotsDecimals int             `json:"baseLotsDecimals"`
	FundingConfig    wsFundingConfig `json:"fundingConfig"`
	LeverageTiers    []leverageTier  `json:"leverageTiers"`
}

type wsFundingConfig struct {
	FundingIntervalSeconds int64 `json:"fundingIntervalSeconds"`
}

type marketStatsV2Envelope struct {
	Channel string              `json:"channel"`
	Stats   []marketStatsV2Wire `json:"stats"`
}

type marketStatsV2Wire struct {
	Symbol             string  `json:"symbol"`
	Timestamp          int64   `json:"timestamp"`
	OpenInterest       float64 `json:"openInterest"`
	MarkPrice          float64 `json:"markPrice"`
	MidPrice           float64 `json:"midPrice"`
	OraclePrice        float64 `json:"oraclePrice"`
	CurrentFundingRate float64 `json:"currentFundingRate"` // Percentage points per funding interval.
}

type orderbookEnvelope struct {
	Channel             string        `json:"channel"`
	Symbol              string        `json:"symbol"`
	Orderbook           orderbookWire `json:"orderbook"`
	BypassExecutionBand bool          `json:"bypassExecutionBand"`
}

type orderbookWire struct {
	Bids [][]float64 `json:"bids"`
	Asks [][]float64 `json:"asks"`
}

func NewMarketStream(client *Client, logger *slog.Logger) *MarketStream {
	if logger == nil {
		logger = slog.Default()
	}
	return &MarketStream{
		client:        client,
		logger:        logger,
		wsURL:         DefaultWSURL,
		dialer:        websocket.DefaultDialer,
		now:           time.Now,
		reconnectWait: websocketReconnectWait,
		markets:       make(map[string]MarketMetadata),
		stats:         make(map[string]LiveMarketStats),
		books:         make(map[string]LiveBook),
		updates:       make(chan struct{}, 1),
	}
}

// Updates signals lifecycle changes that can affect scanner publication.
func (s *MarketStream) Updates() <-chan struct{} { return s.updates }

// Run reconnects the public stream until the context is cancelled.
func (s *MarketStream) Run(ctx context.Context) error {
	if s.client == nil {
		return errors.New("Phoenix market stream requires a metadata client")
	}
	for {
		err := s.connectAndListen(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.logger.Error("phoenix market websocket disconnected, reconnecting", "err", err)
		timer := time.NewTimer(s.reconnectWait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Snapshot returns market state without exposing mutable internal maps or slices.
func (s *MarketStream) Snapshot() MarketStreamSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	markets := make([]MarketMetadata, 0, len(s.markets))
	for _, market := range s.markets {
		markets = append(markets, market)
	}
	sort.Slice(markets, func(i, j int) bool { return markets[i].Symbol < markets[j].Symbol })
	stats := make(map[string]LiveMarketStats, len(s.stats))
	for symbol, state := range s.stats {
		stats[symbol] = state
	}
	books := make(map[string]LiveBook, len(s.books))
	for symbol, state := range s.books {
		books[symbol] = state
	}
	return MarketStreamSnapshot{
		Metadata: ExchangeMetadata{
			Accounts: cloneExchangeAccounts(s.accounts),
			Markets:  markets,
		},
		Stats: stats, Books: books, Connected: s.connected, Synchronized: s.synchronized,
		Generation: s.generation, Sequence: s.sequence,
		MetadataGeneration: s.metadataGeneration, MetadataReceivedAt: s.metadataReceivedAt,
		LastReceivedAt: s.lastReceivedAt,
	}
}

func (s *MarketStream) connectAndListen(ctx context.Context) error {
	metadata, err := s.client.FetchExchangeMetadata(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap Phoenix exchange metadata: %w", err)
	}
	s.replaceMetadata(metadata)

	connection, _, err := s.dialer.DialContext(ctx, s.wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial Phoenix market websocket: %w", err)
	}
	defer connection.Close()
	connection.SetReadLimit(maxWebSocketMessage)
	refreshReadDeadline := func() error {
		return connection.SetReadDeadline(time.Now().Add(websocketReadTimeout))
	}
	if err := refreshReadDeadline(); err != nil {
		return fmt.Errorf("set Phoenix market websocket read deadline: %w", err)
	}
	connection.SetPongHandler(func(string) error { return refreshReadDeadline() })
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(websocketReadTimeout / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = connection.Close()
				return
			case <-done:
				return
			case <-ticker.C:
				// Control writes may run alongside subscription writes. Pongs
				// renew transport liveness only, never market observation ages.
				if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					_ = connection.Close()
					return
				}
			}
		}
	}()

	for _, subscription := range []map[string]any{
		{"type": "subscribe", "subscription": map[string]any{"channel": "exchange", "encoding": "json"}},
		{"type": "subscribe", "subscription": map[string]any{"channel": "marketStatsV2"}},
	} {
		if err := connection.WriteJSON(subscription); err != nil {
			return fmt.Errorf("subscribe Phoenix market websocket: %w", err)
		}
	}
	generation := s.markConnected()
	defer s.markDisconnected(generation)
	orderbooks := make(map[string]bool)

	for {
		_, raw, err := connection.ReadMessage()
		if err != nil {
			return fmt.Errorf("read Phoenix market websocket: %w", err)
		}
		if err := refreshReadDeadline(); err != nil {
			return fmt.Errorf("refresh Phoenix market websocket read deadline: %w", err)
		}
		if err := s.applyMessage(generation, raw); err != nil {
			return err
		}
		if err := s.reconcileOrderbookSubscriptions(connection, orderbooks); err != nil {
			return err
		}
	}
}

func (s *MarketStream) applyMessage(generation uint64, raw []byte) error {
	var envelope wsEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode Phoenix websocket envelope: %w", err)
	}
	switch {
	case envelope.Channel == "exchange":
		var message exchangeEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("decode Phoenix exchange message: %w", err)
		}
		return s.applyExchangeMessage(generation, message)
	case envelope.Channel == "marketStatsV2":
		var message marketStatsV2Envelope
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("decode Phoenix market stats: %w", err)
		}
		return s.applyMarketStats(generation, message)
	case envelope.Channel == "orderbook":
		var message orderbookEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("decode Phoenix orderbook: %w", err)
		}
		return s.applyOrderbook(generation, message)
	case envelope.Channel == "error":
		return fmt.Errorf("Phoenix websocket server error %v: %s", envelope.Code, envelope.Error)
	case envelope.Type == "subscriptionError":
		return fmt.Errorf("Phoenix websocket subscription error %v: %s", envelope.Code, envelope.Message)
	case envelope.Type == "subscriptionConfirmed":
		return nil
	case envelope.Channel == "subscriptionStatus":
		if envelope.Status != "subscribed" && envelope.Status != "unsubscribed" {
			return fmt.Errorf("Phoenix websocket subscription status %q", envelope.Status)
		}
		return nil
	default:
		return nil
	}
}

func (s *MarketStream) applyExchangeMessage(generation uint64, message exchangeEnvelope) error {
	if message.Version != exchangeVersion {
		return fmt.Errorf("unsupported Phoenix exchange version %d", message.Version)
	}
	if message.SequenceNumber == nil {
		return errors.New("Phoenix exchange message is missing sequence number")
	}
	sequence := uint64(*message.SequenceNumber)
	switch message.MessageType {
	case "snapshot":
		metadata, err := normalizeExchangeSnapshot(message.Exchange, message.Markets)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.generation != generation || !s.connected {
			return nil
		}
		if s.haveExchangeSnapshot {
			if sequence < s.sequence {
				return errors.New("Phoenix exchange snapshot sequence regressed")
			}
			if sequence == s.sequence {
				// Phoenix periodically republishes the full exchange on the same
				// connection. Identical metadata is not a stream revision and must
				// neither discard live state nor renew price/book observations.
				if !reflect.DeepEqual(metadata.Accounts, s.accounts) || !reflect.DeepEqual(marketsBySymbol(metadata.Markets), s.markets) {
					return errors.New("Phoenix exchange snapshot conflicts with current sequence")
				}
				return nil
			}
		}
		s.accounts = metadata.Accounts
		s.replaceMarketsLocked(marketsBySymbol(metadata.Markets))
		s.sequence = sequence
		s.lastReceivedAt = s.now().UTC()
		if s.haveExchangeSnapshot {
			// A validated newer full snapshot replaces metadata atomically, as
			// exchange deltas do; its sequence fences existing risk captures.
			s.notify()
		} else {
			s.synchronized = false
		}
		s.haveExchangeSnapshot = true
		return nil
	case "delta":
		return s.applyExchangeDelta(generation, sequence, message.Ops)
	default:
		return fmt.Errorf("unsupported Phoenix exchange message type %q", message.MessageType)
	}
}

func (s *MarketStream) applyExchangeDelta(generation, sequence uint64, rawOps []json.RawMessage) error {
	s.mu.Lock()
	if s.generation != generation || !s.connected {
		s.mu.Unlock()
		return nil
	}
	if !s.haveExchangeSnapshot || s.sequence == math.MaxUint64 || sequence != s.sequence+1 {
		s.mu.Unlock()
		return fmt.Errorf("%w: received %d after %d", errExchangeSequenceGap, sequence, s.sequence)
	}
	accounts := cloneExchangeAccounts(s.accounts)
	markets := cloneMarkets(s.markets)
	for _, raw := range rawOps {
		if err := applyExchangeOp(&accounts, markets, raw); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.accounts = accounts
	s.markets = markets
	s.sequence = sequence
	for symbol := range s.stats {
		market, ok := markets[symbol]
		if !ok {
			delete(s.stats, symbol)
			delete(s.books, symbol)
			continue
		}
		stats := s.stats[symbol]
		stats.HourlyFundingRate = stats.NativeFundingRate / market.FundingInterval.Hours()
		s.stats[symbol] = stats
	}
	for symbol := range s.books {
		market, ok := markets[symbol]
		if !ok || market.Status != "active" {
			delete(s.books, symbol)
		}
	}
	s.lastReceivedAt = s.now().UTC()
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *MarketStream) applyMarketStats(generation uint64, message marketStatsV2Envelope) error {
	if len(message.Stats) == 0 {
		return errors.New("Phoenix market stats update is empty")
	}
	receivedAt := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation || !s.connected {
		return nil
	}
	stats := cloneStats(s.stats)
	applied := 0
	becamePublishable := false
	for _, update := range message.Stats {
		market, known := s.markets[update.Symbol]
		if !known {
			continue
		}
		if update.Timestamp <= 0 || !positiveFinite(update.MarkPrice) || !positiveFinite(update.OraclePrice) ||
			update.OpenInterest < 0 || math.IsNaN(update.OpenInterest) || math.IsInf(update.OpenInterest, 0) ||
			math.IsNaN(update.CurrentFundingRate) || math.IsInf(update.CurrentFundingRate, 0) {
			return fmt.Errorf("invalid Phoenix market stats for %q", update.Symbol)
		}
		if update.MidPrice != 0 && !positiveFinite(update.MidPrice) {
			return fmt.Errorf("invalid Phoenix mid price for %q", update.Symbol)
		}
		sourceTime := time.Unix(update.Timestamp, 0).UTC()
		previous, exists := stats[update.Symbol]
		if exists && sourceTime.Before(previous.SourceTime) {
			continue
		}
		// marketStatsV2 reports percentage points; internal rates are fractions.
		nativeFundingRate := update.CurrentFundingRate / 100
		stats[update.Symbol] = LiveMarketStats{
			Symbol: update.Symbol, MarkPrice: update.MarkPrice, MidPrice: update.MidPrice,
			IndexPrice:        update.OraclePrice,
			HourlyFundingRate: nativeFundingRate / market.FundingInterval.Hours(),
			NativeFundingRate: nativeFundingRate, OpenInterest: update.OpenInterest,
			SourceTime: sourceTime, ReceivedAt: receivedAt, Generation: generation,
		}
		if !exists {
			_, hasBook := s.books[update.Symbol]
			becamePublishable = becamePublishable || hasBook
		}
		applied++
	}
	if applied > 0 && s.haveExchangeSnapshot {
		becameSynchronized := !s.synchronized
		s.stats = stats
		s.synchronized = true
		s.lastReceivedAt = receivedAt
		if becameSynchronized || becamePublishable {
			s.notify()
		}
	}
	return nil
}

func (s *MarketStream) applyOrderbook(generation uint64, message orderbookEnvelope) error {
	if message.BypassExecutionBand {
		return fmt.Errorf("Phoenix orderbook for %q bypasses the execution band", message.Symbol)
	}
	if len(message.Orderbook.Bids) == 0 || len(message.Orderbook.Asks) == 0 {
		s.mu.Lock()
		changed := false
		if s.generation == generation && s.connected {
			_, changed = s.books[message.Symbol]
			delete(s.books, message.Symbol)
			s.lastReceivedAt = s.now().UTC()
		}
		s.mu.Unlock()
		if changed {
			s.notify()
		}
		return nil
	}
	bid, ask := message.Orderbook.Bids[0], message.Orderbook.Asks[0]
	if len(bid) != 2 || len(ask) != 2 || !positiveFinite(bid[0]) || !positiveFinite(bid[1]) ||
		!positiveFinite(ask[0]) || !positiveFinite(ask[1]) || ask[0] < bid[0] {
		return fmt.Errorf("invalid Phoenix orderbook BBO for %q", message.Symbol)
	}
	bidSizeUSD, askSizeUSD := bid[0]*bid[1], ask[0]*ask[1]
	if !positiveFinite(bidSizeUSD) || !positiveFinite(askSizeUSD) {
		return fmt.Errorf("invalid Phoenix orderbook notional for %q", message.Symbol)
	}
	receivedAt := s.now().UTC()
	s.mu.Lock()
	if s.generation != generation || !s.connected {
		s.mu.Unlock()
		return nil
	}
	market, known := s.markets[message.Symbol]
	if !known || market.Status != "active" {
		s.mu.Unlock()
		return nil
	}
	_, existed := s.books[message.Symbol]
	s.books[message.Symbol] = LiveBook{
		Symbol: message.Symbol, BidPrice: bid[0], BidSizeUSD: bidSizeUSD,
		AskPrice: ask[0], AskSizeUSD: askSizeUSD, ReceivedAt: receivedAt, Generation: generation,
	}
	s.lastReceivedAt = receivedAt
	synchronized := s.synchronized
	s.mu.Unlock()
	if !existed && synchronized {
		s.notify()
	}
	return nil
}

func (s *MarketStream) reconcileOrderbookSubscriptions(connection *websocket.Conn, subscribed map[string]bool) error {
	s.mu.RLock()
	active := make(map[string]bool)
	if s.haveExchangeSnapshot {
		for symbol, market := range s.markets {
			if market.Status == "active" {
				active[symbol] = true
			}
		}
	}
	s.mu.RUnlock()
	for symbol := range subscribed {
		if active[symbol] {
			continue
		}
		if err := writeOrderbookSubscription(connection, "unsubscribe", symbol); err != nil {
			return err
		}
		delete(subscribed, symbol)
	}
	for symbol := range active {
		if subscribed[symbol] {
			continue
		}
		if err := writeOrderbookSubscription(connection, "subscribe", symbol); err != nil {
			return err
		}
		subscribed[symbol] = true
	}
	return nil
}

func writeOrderbookSubscription(connection *websocket.Conn, action, symbol string) error {
	if err := connection.WriteJSON(map[string]any{
		"type": action, "subscription": map[string]any{"channel": "orderbook", "symbol": symbol},
	}); err != nil {
		return fmt.Errorf("%s Phoenix orderbook %q: %w", action, symbol, err)
	}
	return nil
}

func (s *MarketStream) replaceMetadata(metadata ExchangeMetadata) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = cloneExchangeAccounts(metadata.Accounts)
	s.replaceMarketsLocked(marketsBySymbol(metadata.Markets))
	s.metadataGeneration++
	s.metadataReceivedAt = s.now().UTC()
}

func (s *MarketStream) replaceMarketsLocked(markets map[string]MarketMetadata) {
	s.markets = markets
	for symbol, stats := range s.stats {
		market, ok := markets[symbol]
		if !ok {
			delete(s.stats, symbol)
			continue
		}
		stats.HourlyFundingRate = stats.NativeFundingRate / market.FundingInterval.Hours()
		s.stats[symbol] = stats
	}
	for symbol := range s.books {
		market, ok := markets[symbol]
		if !ok || market.Status != "active" {
			delete(s.books, symbol)
		}
	}
}

func (s *MarketStream) markConnected() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.connected = true
	s.synchronized = false
	s.sequence = 0
	s.haveExchangeSnapshot = false
	s.lastReceivedAt = time.Time{}
	return s.generation
}

func (s *MarketStream) markDisconnected(generation uint64) {
	s.mu.Lock()
	changed := false
	if s.generation == generation {
		changed = s.connected || s.synchronized
		s.connected = false
		s.synchronized = false
	}
	s.mu.Unlock()
	if changed {
		s.notify()
	}
}

func (s *MarketStream) notify() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

func normalizeExchangeSnapshot(accounts wsExchangeAccounts, configs []wsMarketConfig) (ExchangeMetadata, error) {
	if accounts.PerpAssetMap == "" || len(accounts.GlobalTraderIndex) == 0 || len(accounts.ActiveTraderBuffer) == 0 {
		return ExchangeMetadata{}, errors.New("Phoenix exchange snapshot is missing required accounts")
	}
	if len(configs) == 0 {
		return ExchangeMetadata{}, errors.New("Phoenix exchange snapshot contains no markets")
	}
	markets := make([]MarketMetadata, 0, len(configs))
	for _, config := range configs {
		market, err := normalizeWSMarket(config)
		if err != nil {
			return ExchangeMetadata{}, fmt.Errorf("normalize Phoenix websocket market %q: %w", config.Symbol, err)
		}
		markets = append(markets, market)
	}
	sort.Slice(markets, func(i, j int) bool { return markets[i].Symbol < markets[j].Symbol })
	return ExchangeMetadata{
		Accounts: ExchangeAccounts{
			ProtocolRootAuthority: accounts.CurrentAuthorities.RootAuthority,
			PerpAssetMap:          accounts.PerpAssetMap, GlobalTraderIndex: append([]string(nil), accounts.GlobalTraderIndex...),
			ActiveTraderBuffer: append([]string(nil), accounts.ActiveTraderBuffer...),
		},
		Markets: markets,
	}, nil
}

func normalizeWSMarket(config wsMarketConfig) (MarketMetadata, error) {
	return normalizeMarketConfig(marketConfig{
		Symbol: config.Symbol, MarketStatus: config.MarketStatus,
		MarketPubkey: config.MarketPubkey, SplinePubkey: config.SplinePubkey,
		TickSize: config.TickSize, BaseLotsDecimals: config.BaseLotsDecimals,
		FundingIntervalSeconds: config.FundingConfig.FundingIntervalSeconds,
		LeverageTiers:          config.LeverageTiers,
	})
}

func applyExchangeOp(accounts *ExchangeAccounts, markets map[string]MarketMetadata, raw json.RawMessage) error {
	var op struct {
		Kind                 string             `json:"kind"`
		Symbol               string             `json:"symbol"`
		Market               wsMarketConfig     `json:"market"`
		Exchange             wsExchangeAccounts `json:"exchange"`
		NewMarketStatus      string             `json:"new_market_status"`
		NewMarketStatusCamel string             `json:"newMarketStatus"`
		Update               json.RawMessage    `json:"update"`
	}
	if err := json.Unmarshal(raw, &op); err != nil {
		return fmt.Errorf("decode Phoenix exchange delta op: %w", err)
	}
	switch op.Kind {
	case "exchangeKeysUpdated":
		if op.Exchange.PerpAssetMap == "" || len(op.Exchange.GlobalTraderIndex) == 0 || len(op.Exchange.ActiveTraderBuffer) == 0 {
			return errors.New("Phoenix exchange keys delta is missing required accounts")
		}
		*accounts = ExchangeAccounts{
			ProtocolRootAuthority: op.Exchange.CurrentAuthorities.RootAuthority,
			PerpAssetMap:          op.Exchange.PerpAssetMap, GlobalTraderIndex: append([]string(nil), op.Exchange.GlobalTraderIndex...),
			ActiveTraderBuffer: append([]string(nil), op.Exchange.ActiveTraderBuffer...),
		}
	case "marketAdded":
		market, err := normalizeWSMarket(op.Market)
		if err != nil {
			return fmt.Errorf("normalize added Phoenix market %q: %w", op.Market.Symbol, err)
		}
		markets[market.Symbol] = market
	case "marketStatusChanged":
		status := op.NewMarketStatus
		if status == "" {
			status = op.NewMarketStatusCamel
		}
		if err := updateMarketStatus(markets, op.Symbol, status); err != nil {
			return err
		}
	case "marketClosed":
		if err := updateMarketStatus(markets, op.Symbol, "closed"); err != nil {
			return err
		}
	case "marketTombstoned":
		if err := updateMarketStatus(markets, op.Symbol, "tombstoned"); err != nil {
			return err
		}
	case "marketDeleted":
		delete(markets, op.Symbol)
	case "marketParameterUpdated":
		if err := applyMarketParameterUpdate(markets, op.Symbol, op.Update); err != nil {
			return err
		}
	}
	return nil
}

func applyMarketParameterUpdate(markets map[string]MarketMetadata, symbol string, raw json.RawMessage) error {
	market, ok := markets[symbol]
	if !ok {
		return fmt.Errorf("Phoenix market parameter delta references unknown market %q", symbol)
	}
	var update struct {
		Kind string          `json:"kind"`
		New  json.RawMessage `json:"new"`
	}
	if err := json.Unmarshal(raw, &update); err != nil {
		return fmt.Errorf("decode Phoenix market parameter update for %q: %w", symbol, err)
	}
	switch update.Kind {
	case "leverageTiersUpdated":
		var tiers []leverageTier
		if err := json.Unmarshal(update.New, &tiers); err != nil {
			return fmt.Errorf("decode Phoenix leverage tiers for %q: %w", symbol, err)
		}
		maxLeverage := 0.0
		for _, tier := range tiers {
			maxLeverage = max(maxLeverage, tier.MaxLeverage)
		}
		if !positiveFinite(maxLeverage) {
			return fmt.Errorf("invalid Phoenix leverage tiers for %q", symbol)
		}
		market.MaxLeverage = int(math.Floor(maxLeverage))
	case "fundingParametersUpdated":
		var funding wsFundingConfig
		if err := json.Unmarshal(update.New, &funding); err != nil {
			return fmt.Errorf("decode Phoenix funding parameters for %q: %w", symbol, err)
		}
		if funding.FundingIntervalSeconds <= 0 {
			return fmt.Errorf("invalid Phoenix funding interval for %q", symbol)
		}
		market.FundingInterval = time.Duration(funding.FundingIntervalSeconds) * time.Second
	}
	markets[symbol] = market
	return nil
}

func updateMarketStatus(markets map[string]MarketMetadata, symbol, status string) error {
	market, ok := markets[symbol]
	if !ok {
		return fmt.Errorf("Phoenix status delta references unknown market %q", symbol)
	}
	if !validMarketStatus(status) {
		return fmt.Errorf("unknown Phoenix market status %q", status)
	}
	market.Status = status
	markets[symbol] = market
	return nil
}

func marketsBySymbol(markets []MarketMetadata) map[string]MarketMetadata {
	result := make(map[string]MarketMetadata, len(markets))
	for _, market := range markets {
		result[market.Symbol] = market
	}
	return result
}

func cloneMarkets(markets map[string]MarketMetadata) map[string]MarketMetadata {
	result := make(map[string]MarketMetadata, len(markets))
	for symbol, market := range markets {
		result[symbol] = market
	}
	return result
}

func cloneStats(stats map[string]LiveMarketStats) map[string]LiveMarketStats {
	result := make(map[string]LiveMarketStats, len(stats))
	for symbol, state := range stats {
		result[symbol] = state
	}
	return result
}

func cloneExchangeAccounts(accounts ExchangeAccounts) ExchangeAccounts {
	return ExchangeAccounts{
		ProtocolRootAuthority: accounts.ProtocolRootAuthority,
		PerpAssetMap:          accounts.PerpAssetMap, GlobalTraderIndex: append([]string(nil), accounts.GlobalTraderIndex...),
		ActiveTraderBuffer: append([]string(nil), accounts.ActiveTraderBuffer...),
	}
}
