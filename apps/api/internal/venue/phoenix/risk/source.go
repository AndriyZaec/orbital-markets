// Package risk joins complete Phoenix risk parameters with the existing live
// market stream. It performs schema/unit normalization, never margin math.
package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
)

const (
	paramsTTL        = time.Minute
	readTimeout      = 5 * time.Second
	retryDelay       = 5 * time.Second
	maxResponseBytes = 4 * 1024 * 1024
)

type MarketStream interface {
	Snapshot() phoenix.MarketStreamSnapshot
}

type revision struct{ generation, sequence uint64 }

func streamRevision(s phoenix.MarketStreamSnapshot) revision {
	return revision{s.Generation, s.Sequence}
}
func synchronized(s phoenix.MarketStreamSnapshot) bool {
	return s.Connected && s.Synchronized && s.Generation != 0
}

type parameters struct {
	revision    revision
	generation  uint64
	observedAt  time.Time
	markets     map[string]marketConfig
	assets      map[uint32]collateralAsset
	symbolsByID map[uint32]string
}

type refresh struct {
	done   chan struct{}
	params *parameters
	err    error
}

// Source is shared process-wide across account feeds. It makes two bounded
// catalog reads, not per-market reads, and caches only parameter observations.
// Price freshness is always checked against the current stream capture.
type Source struct {
	baseURL    string
	http       *http.Client
	stream     MarketStream
	now        func() time.Time
	mu         sync.Mutex
	cached     *parameters
	inflight   *refresh
	nextRead   time.Time
	generation uint64
}

func NewSource(baseURL string, client *http.Client, stream MarketStream) *Source {
	if baseURL == "" {
		baseURL = phoenix.DefaultRESTURL
	}
	if client == nil {
		client = &http.Client{Timeout: readTimeout}
	}
	return &Source{baseURL: strings.TrimRight(baseURL, "/"), http: client, stream: stream, now: time.Now}
}

// Evaluate binds the joined inputs to live stream continuity before and after
// helper computation. Neither parameter cache hits nor pongs refresh prices.
func (s *Source) Evaluate(ctx context.Context, state *account.State, computer account.RiskComputer) (helper.RiskResult, error) {
	if s.stream == nil || state == nil {
		return helper.RiskResult{}, errors.New("Phoenix risk sources unavailable")
	}
	before := s.stream.Snapshot()
	inputs, err := s.inputs(ctx, state.Snapshot(), before)
	if err != nil {
		return helper.RiskResult{}, err
	}
	result, err := state.Risk(ctx, computer, inputs)
	if err != nil {
		return helper.RiskResult{}, err
	}
	after := s.stream.Snapshot()
	if !synchronized(after) || streamRevision(before) != streamRevision(after) {
		return helper.RiskResult{}, fmt.Errorf("%w: exchange changed during risk computation", account.ErrRiskSuperseded)
	}
	return result, nil
}

// ProjectOpen includes a target market even when the account is otherwise flat.
// It returns a bounded model result, not permission to sign or submit an order.
func (s *Source) ProjectOpen(ctx context.Context, state *account.State, computer account.RiskComputer, order helper.OrderProjection) (helper.RiskResult, error) {
	if s.stream == nil || state == nil {
		return helper.RiskResult{}, errors.New("Phoenix projection sources unavailable")
	}
	if err := helper.ValidateProjectionIntent(order); err != nil {
		return helper.RiskResult{}, err
	}
	before := s.stream.Snapshot()
	active := false
	for _, market := range before.Metadata.Markets {
		if market.Symbol == order.Symbol {
			active = market.Status == "active"
			break
		}
	}
	if !active {
		return helper.RiskResult{}, errors.New("Phoenix projected market is not active")
	}
	inputs, err := s.inputs(ctx, state.Snapshot(), before, order.Symbol)
	if err != nil {
		return helper.RiskResult{}, err
	}
	for _, target := range inputs.Markets {
		if target.Symbol == order.Symbol && (target.TakerFeeRate == nil || target.IsolatedOnly == nil || *target.IsolatedOnly) {
			return helper.RiskResult{}, errors.New("Phoenix opening fee or cross-margin policy unavailable")
		}
	}
	result, err := state.ProjectOpen(ctx, computer, inputs, order)
	if err != nil {
		return helper.RiskResult{}, err
	}
	after := s.stream.Snapshot()
	if !synchronized(after) || streamRevision(before) != streamRevision(after) {
		return helper.RiskResult{}, errors.New("Phoenix exchange changed during order projection")
	}
	return result, nil
}

// ParameterObservation fences a completed projection to the exact still-current
// REST parameter generation and stream revision. It never fetches or refreshes
// evidence while validating an order capture.
func (s *Source) ParameterObservation(generation, streamGeneration, sequence uint64) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.cached
	if p == nil || p.generation != generation || p.revision != (revision{streamGeneration, sequence}) || !fresh(p.observedAt, s.now(), paramsTTL) {
		return time.Time{}, errors.New("Phoenix order risk parameters changed or expired")
	}
	return p.observedAt, nil
}

func (s *Source) RefreshParameters(ctx context.Context) error {
	if s.stream == nil {
		return errors.New("Phoenix market stream unavailable")
	}
	before := s.stream.Snapshot()
	if !synchronized(before) {
		return errors.New("Phoenix market stream not synchronized")
	}
	if _, err := s.parameters(ctx, streamRevision(before)); err != nil {
		return err
	}
	after := s.stream.Snapshot()
	if !synchronized(after) || streamRevision(after) != streamRevision(before) {
		return errors.New("Phoenix exchange changed during parameter refresh")
	}
	return nil
}

func (s *Source) inputs(ctx context.Context, acct account.Snapshot, stream phoenix.MarketStreamSnapshot, extraSymbols ...string) (account.RiskInputs, error) {
	if !acct.Fresh(s.now()) || !synchronized(stream) {
		return account.RiskInputs{}, errors.New("Phoenix account or market stream not synchronized")
	}
	p, err := s.parameters(ctx, streamRevision(stream))
	if err != nil {
		return account.RiskInputs{}, err
	}
	// Catalog reads may cross a reconnect or parameter update. Such a capture
	// cannot be combined with the pre-read prices, even if REST itself succeeded.
	after := s.stream.Snapshot()
	if !synchronized(after) || streamRevision(after) != streamRevision(stream) {
		return account.RiskInputs{}, fmt.Errorf("%w: exchange changed during parameter read", account.ErrRiskSuperseded)
	}
	needed := make(map[string]bool)
	for _, symbol := range extraSymbols {
		if symbol == "" {
			return account.RiskInputs{}, errors.New("missing projected market symbol")
		}
		needed[symbol] = true
	}
	for symbol := range acct.Cross.Positions {
		needed[symbol] = true
	}
	for symbol, orders := range acct.Cross.Orders {
		if len(orders) != 0 {
			needed[symbol] = true
		}
	}
	result := account.RiskInputs{MetadataGeneration: p.generation, ParamsObservedAt: p.observedAt, Markets: []helper.RiskMarket{}, SpotParams: []helper.RiskSpotParams{}}
	for _, balance := range acct.Cross.SpotCollaterals {
		value, err := strconv.ParseUint(balance.Balance, 10, 64)
		if err != nil {
			return account.RiskInputs{}, errors.New("invalid spot collateral balance")
		}
		if value == 0 {
			continue
		}
		asset, exists := p.assets[balance.AssetIndex]
		if !exists || asset.Symbol != balance.Symbol || asset.Decimals == nil || *asset.Decimals != balance.Decimals || asset.Spot == nil || asset.Spot.Active == nil || !*asset.Spot.Active || asset.Spot.PerpAssetIndex == nil {
			return account.RiskInputs{}, errors.New("Phoenix collateral configuration unavailable or mismatched")
		}
		pricing, exists := p.symbolsByID[*asset.Spot.PerpAssetIndex]
		if !exists {
			return account.RiskInputs{}, errors.New("unknown Phoenix collateral pricing market")
		}
		spot, err := normalizeSpot(asset, pricing)
		if err != nil {
			return account.RiskInputs{}, err
		}
		result.SpotParams = append(result.SpotParams, spot)
		needed[pricing] = true
	}
	currentMarkets := make(map[string]phoenix.MarketMetadata, len(stream.Metadata.Markets))
	for _, m := range stream.Metadata.Markets {
		currentMarkets[m.Symbol] = m
	}
	for symbol := range needed {
		config, exists := p.markets[symbol]
		if !exists {
			return account.RiskInputs{}, fmt.Errorf("Phoenix risk parameters missing for %s", symbol)
		}
		current, exists := currentMarkets[symbol]
		if !exists {
			return account.RiskInputs{}, fmt.Errorf("Phoenix stream market missing for %s", symbol)
		}
		market, err := normalizeMarket(config)
		if err != nil {
			return account.RiskInputs{}, fmt.Errorf("Phoenix risk parameters for %s: %w", symbol, err)
		}
		amountStep := math.Pow10(-int(market.BaseLotDecimals))
		priceStep := float64(*config.TickSize) * math.Pow10(int(market.BaseLotDecimals)) / 1_000_000
		maxLeverage := uint64(0)
		for _, tier := range market.LeverageTiers {
			maxLeverage = max(maxLeverage, tier.MaxLeverage)
		}
		if current.AmountIncrement != amountStep || current.PriceIncrement != priceStep || current.MaxLeverage != int(maxLeverage) || current.Status != config.Status {
			return account.RiskInputs{}, fmt.Errorf("Phoenix REST/stream market parameters disagree for %s", symbol)
		}
		stats, exists := stream.Stats[symbol]
		now := s.now()
		if !exists || stats.Generation != stream.Generation || !fresh(stats.SourceTime, now, helper.RiskDataMaxAge) || !fresh(stats.ReceivedAt, now, helper.RiskDataMaxAge) || !positive(stats.MarkPrice) || !positive(stats.IndexPrice) {
			return account.RiskInputs{}, fmt.Errorf("Phoenix risk prices missing or stale for %s", symbol)
		}
		// SDK conversion owns tick quantization. Index is never replaced by mark.
		mark, index := stats.MarkPrice, stats.IndexPrice
		market.MarkPriceUSD, market.IndexPriceUSD = &mark, &index
		market.PriceObservedUnixMS = min(stats.SourceTime.UnixMilli(), stats.ReceivedAt.UnixMilli())
		result.Markets = append(result.Markets, market)
	}
	sort.Slice(result.Markets, func(i, j int) bool { return result.Markets[i].Symbol < result.Markets[j].Symbol })
	sort.Slice(result.SpotParams, func(i, j int) bool { return result.SpotParams[i].AssetIndex < result.SpotParams[j].AssetIndex })
	return result, nil
}

func fresh(observed, now time.Time, age time.Duration) bool {
	return !observed.IsZero() && !now.Before(observed) && now.Sub(observed) <= age
}
func positive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func (s *Source) parameters(ctx context.Context, rev revision) (*parameters, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.cached != nil && s.cached.revision == rev && fresh(s.cached.observedAt, s.now(), paramsTTL) {
		p := s.cached
		s.mu.Unlock()
		return p, nil
	}
	f := s.inflight
	if f == nil {
		if s.now().Before(s.nextRead) {
			s.mu.Unlock()
			return nil, errors.New("Phoenix risk parameters backing off")
		}
		f = &refresh{done: make(chan struct{})}
		s.inflight = f
		go func() {
			p, err := s.readParameters(context.WithoutCancel(ctx), rev)
			s.mu.Lock()
			if err == nil {
				s.generation++
				p.generation = s.generation
				s.cached = p
			} else {
				s.nextRead = maxTime(s.nextRead, s.now().Add(retryDelay))
			}
			f.params, f.err = p, err
			s.inflight = nil
			close(f.done)
			s.mu.Unlock()
		}()
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		if f.err != nil {
			return nil, f.err
		}
		if f.params.revision != rev {
			return nil, fmt.Errorf("%w: parameter generation changed", account.ErrRiskSuperseded)
		}
		return f.params, nil
	}
}

func (s *Source) readParameters(ctx context.Context, rev revision) (*parameters, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*readTimeout)
	defer cancel()
	p := &parameters{revision: rev, observedAt: s.now(), markets: make(map[string]marketConfig), assets: make(map[uint32]collateralAsset), symbolsByID: make(map[uint32]string)}
	var exchange struct {
		Markets []marketConfig `json:"markets"`
	}
	if err := s.get(ctx, "/v1/view/exchange", &exchange); err != nil {
		return nil, err
	}
	if len(exchange.Markets) == 0 {
		return nil, errors.New("empty Phoenix risk catalog")
	}
	for _, market := range exchange.Markets {
		if market.Symbol == "" || market.AssetID == nil {
			return nil, errors.New("missing Phoenix market identity")
		}
		if _, exists := p.markets[market.Symbol]; exists {
			return nil, errors.New("duplicate Phoenix risk symbol")
		}
		if _, exists := p.symbolsByID[*market.AssetID]; exists {
			return nil, errors.New("duplicate Phoenix risk asset ID")
		}
		p.markets[market.Symbol], p.symbolsByID[*market.AssetID] = market, market.Symbol
	}
	var collateral struct {
		Assets []collateralAsset `json:"assets"`
	}
	if err := s.get(ctx, "/v1/collateral/assets", &collateral); err != nil {
		return nil, err
	}
	if len(collateral.Assets) == 0 {
		return nil, errors.New("empty Phoenix collateral catalog")
	}
	for _, asset := range collateral.Assets {
		if asset.AssetIndex == nil || asset.Symbol == "" {
			return nil, errors.New("missing Phoenix collateral identity")
		}
		if _, exists := p.assets[*asset.AssetIndex]; exists {
			return nil, errors.New("duplicate Phoenix collateral asset")
		}
		p.assets[*asset.AssetIndex] = asset
	}
	return p, nil
}

func (s *Source) get(ctx context.Context, path string, target any) error {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return errors.New("invalid Phoenix risk endpoint")
	}
	response, err := s.http.Do(request)
	if err != nil {
		return errors.New("Phoenix risk parameter request failed")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		delay := retryDelay
		if seconds, err := strconv.ParseUint(response.Header.Get("Retry-After"), 10, 32); err == nil {
			delay = max(delay, time.Duration(seconds)*time.Second)
		} else if date, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
			delay = max(delay, date.Sub(s.now()))
		}
		s.mu.Lock()
		s.nextRead = maxTime(s.nextRead, s.now().Add(delay))
		s.mu.Unlock()
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Phoenix risk parameters HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return errors.New("Phoenix risk parameter response unavailable or too large")
	}
	return json.Unmarshal(raw, target)
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Canonical BPS fields take precedence; legacy percentages are accepted only
// when they convert exactly to a basis-point integer. Missing is not zero.
type factor struct {
	bps     *uint16
	percent *float64
}

func (f factor) value() (uint16, error) {
	if f.bps != nil {
		if *f.bps > 10000 {
			return 0, errors.New("invalid risk bps")
		}
		return *f.bps, nil
	}
	if f.percent == nil || math.IsNaN(*f.percent) || math.IsInf(*f.percent, 0) || *f.percent < 0 || *f.percent > 100 {
		return 0, errors.New("missing or invalid risk factor")
	}
	value := *f.percent * 100
	if math.Abs(value-math.Round(value)) > 1e-8 {
		return 0, errors.New("risk factor cannot be represented in basis points")
	}
	return uint16(math.Round(value)), nil
}

type tier struct {
	MaxLeverage   *float64 `json:"maxLeverage"`
	MaxSize       *uint64  `json:"maxSizeBaseLots"`
	RiskFactor    *float64 `json:"limitOrderRiskFactor"`
	RiskFactorBPS *uint16  `json:"limitOrderRiskFactorBps"`
}
type marketConfig struct {
	Symbol       string   `json:"symbol"`
	Status       string   `json:"marketStatus"`
	AssetID      *uint32  `json:"assetId"`
	TickSize     *uint64  `json:"tickSize"`
	Decimals     *int8    `json:"baseLotsDecimals"`
	TakerFee     *float64 `json:"takerFee"`
	IsolatedOnly *bool    `json:"isolatedOnly"`
	Tiers        []tier   `json:"leverageTiers"`
	Risk         struct {
		Maintenance    *float64 `json:"maintenance"`
		MaintenanceBPS *uint16  `json:"maintenanceBps"`
		Backstop       *float64 `json:"backstop"`
		BackstopBPS    *uint16  `json:"backstopBps"`
		HighRisk       *float64 `json:"highRisk"`
		HighRiskBPS    *uint16  `json:"highRiskBps"`
		UPnL           *float64 `json:"upnl"`
		UPnLBPS        *uint16  `json:"upnlBps"`
		Withdrawal     *float64 `json:"upnlForWithdrawals"`
		WithdrawalBPS  *uint16  `json:"upnlForWithdrawalsBps"`
		Cancel         *float64 `json:"cancelOrder"`
		CancelBPS      *uint16  `json:"cancelOrderBps"`
	} `json:"riskFactors"`
}
type collateralAsset struct {
	AssetIndex *uint32 `json:"assetIndex"`
	Symbol     string  `json:"symbol"`
	Decimals   *uint8  `json:"decimals"`
	Spot       *struct {
		Active           *bool      `json:"isActive"`
		PerpAssetIndex   *uint32    `json:"perpAssetIndex"`
		MaxGlobalBalance wireUint64 `json:"maxGlobalBalance"`
		MinDiscount      *uint16    `json:"minMarginDiscountBps"`
		MaxDiscount      *uint16    `json:"maxMarginDiscountBps"`
	} `json:"spot"`
}

func normalizeMarket(m marketConfig) (helper.RiskMarket, error) {
	if m.AssetID == nil || m.Decimals == nil || *m.Decimals < -18 || *m.Decimals > 18 || m.TickSize == nil || *m.TickSize == 0 || len(m.Tiers) != 4 {
		return helper.RiskMarket{}, errors.New("incomplete market risk configuration")
	}
	r := helper.RiskMarket{Symbol: m.Symbol, AssetID: uint64(*m.AssetID), BaseLotDecimals: *m.Decimals, TickSize: strconv.FormatUint(*m.TickSize, 10)}
	if m.TakerFee != nil {
		if math.IsNaN(*m.TakerFee) || math.IsInf(*m.TakerFee, 0) || *m.TakerFee < 0 || *m.TakerFee > 1 {
			return helper.RiskMarket{}, errors.New("invalid Phoenix taker fee")
		}
		fee := *m.TakerFee
		r.TakerFeeRate = &fee
	}
	if m.IsolatedOnly != nil {
		isolated := *m.IsolatedOnly
		r.IsolatedOnly = &isolated
	}
	for i, t := range m.Tiers {
		if t.MaxLeverage == nil || !positive(*t.MaxLeverage) || *t.MaxLeverage != math.Trunc(*t.MaxLeverage) || *t.MaxLeverage >= float64(math.MaxInt64) || t.MaxSize == nil || *t.MaxSize == 0 || *t.MaxSize > math.MaxUint32 {
			return helper.RiskMarket{}, errors.New("invalid or unrepresentable leverage tier")
		}
		bps, err := (factor{t.RiskFactorBPS, t.RiskFactor}).value()
		if err != nil {
			return helper.RiskMarket{}, err
		}
		r.LeverageTiers[i] = helper.RiskTierInput{UpperBoundBaseLots: strconv.FormatUint(*t.MaxSize, 10), MaxLeverage: uint64(*t.MaxLeverage), LimitOrderRiskFactorBPS: bps}
		if i > 0 && (*t.MaxSize <= *m.Tiers[i-1].MaxSize || *t.MaxLeverage > *m.Tiers[i-1].MaxLeverage || bps < r.LeverageTiers[i-1].LimitOrderRiskFactorBPS) {
			return helper.RiskMarket{}, errors.New("invalid leverage tier ordering")
		}
	}
	factors := []factor{{m.Risk.MaintenanceBPS, m.Risk.Maintenance}, {m.Risk.BackstopBPS, m.Risk.Backstop}, {m.Risk.HighRiskBPS, m.Risk.HighRisk}, {m.Risk.CancelBPS, m.Risk.Cancel}, {m.Risk.UPnLBPS, m.Risk.UPnL}, {m.Risk.WithdrawalBPS, m.Risk.Withdrawal}}
	values := make([]uint16, len(factors))
	for i, f := range factors {
		value, err := f.value()
		if err != nil {
			return helper.RiskMarket{}, err
		}
		values[i] = value
	}
	copy(r.RiskFactorsBPS[:], values[:3])
	r.CancelOrderRiskFactorBPS, r.UPnLRiskFactorBPS, r.UPnLWithdrawalRiskFactorBPS = values[3], values[4], values[5]
	return r, nil
}

// JsSafeU64 accepts either a JSON integer or a decimal string in the SDK.
// Parse directly to uint64 so values above 2^53 never pass through a float.
type wireUint64 string

func (v *wireUint64) UnmarshalJSON(raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "\"") {
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return err
	}
	*v = wireUint64(strconv.FormatUint(parsed, 10))
	return nil
}

func normalizeSpot(a collateralAsset, pricing string) (helper.RiskSpotParams, error) {
	p := a.Spot
	cap, err := strconv.ParseUint(string(p.MaxGlobalBalance), 10, 64)
	if err != nil || cap == 0 || p.MinDiscount == nil || p.MaxDiscount == nil || *p.MinDiscount > *p.MaxDiscount || *p.MaxDiscount > 10000 {
		return helper.RiskSpotParams{}, errors.New("invalid Phoenix collateral risk configuration")
	}
	return helper.RiskSpotParams{AssetIndex: *a.AssetIndex, Symbol: a.Symbol, PricingMarketSymbol: pricing, Decimals: *a.Decimals, MaxGlobalBalance: string(p.MaxGlobalBalance), MinMarginDiscountBPS: *p.MinDiscount, MaxMarginDiscountBPS: *p.MaxDiscount}, nil
}
