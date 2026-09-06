package aster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	venueName        = "aster"
	defaultRESTURL   = "https://fapi.asterdex.com"
	maxResponseBytes = 8 << 20
	maxSnapshotAge   = 30 * time.Second
	maxClockSkew     = 5 * time.Second
)

type exchangeInfoResponse struct {
	Symbols []struct {
		Symbol       string `json:"symbol"`
		ContractType string `json:"contractType"`
		Status       string `json:"status"`
		BaseAsset    string `json:"baseAsset"`
		QuoteAsset   string `json:"quoteAsset"`
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

// Adapter polls Aster's public Futures V3 REST API and atomically publishes
// normalized market snapshots. Execution wiring is intentionally separate.
type Adapter struct {
	mu        sync.RWMutex
	snapshots map[string]venue.MarketData
	logger    *slog.Logger
	client    *http.Client
	restURL   string
}

func New(logger *slog.Logger) *Adapter {
	return &Adapter{
		snapshots: make(map[string]venue.MarketData),
		logger:    logger,
		client:    &http.Client{Timeout: 10 * time.Second},
		restURL:   defaultRESTURL,
	}
}

func (a *Adapter) Name() string { return venueName }

func (a *Adapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	symbols := make([]string, 0, len(a.snapshots))
	for symbol := range a.snapshots {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	out := make([]venue.MarketData, 0, len(symbols))
	now := time.Now()
	for _, symbol := range symbols {
		snapshot := a.snapshots[symbol]
		if snapshot.Timestamp.IsZero() || now.Sub(snapshot.Timestamp) > maxSnapshotAge || snapshot.Timestamp.After(now.Add(maxClockSkew)) {
			continue
		}
		out = append(out, snapshot)
	}
	return out, nil
}

// Refresh fetches one complete REST snapshot. Callers own scheduling; startup
// wiring can choose an appropriate REST/stream strategy without hidden polling.
func (a *Adapter) Refresh(ctx context.Context) error {
	snapshots, err := a.fetchSnapshots(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.snapshots = snapshots
	a.mu.Unlock()
	a.logger.Debug("aster market data updated", "symbols", len(snapshots))
	return nil
}

func (a *Adapter) fetchSnapshots(ctx context.Context) (map[string]venue.MarketData, error) {
	var exchange exchangeInfoResponse
	var premiums []premiumIndex
	var books []bookTicker
	var funding []fundingInfo
	for _, request := range []struct {
		path   string
		target any
	}{
		{path: "/fapi/v3/exchangeInfo", target: &exchange},
		{path: "/fapi/v3/premiumIndex", target: &premiums},
		{path: "/fapi/v3/ticker/bookTicker", target: &books},
		{path: "/fapi/v3/fundingInfo", target: &funding},
	} {
		if err := a.getJSON(ctx, request.path, request.target); err != nil {
			return nil, err
		}
	}

	assets := make(map[string]string)
	for _, symbol := range exchange.Symbols {
		if symbol.Status == "TRADING" && symbol.ContractType == "PERPETUAL" && symbol.QuoteAsset == "USDT" {
			assets[symbol.Symbol] = symbol.BaseAsset
		}
	}
	intervals := make(map[string]int, len(funding))
	for _, item := range funding {
		if item.FundingIntervalHours > 0 {
			intervals[item.Symbol] = item.FundingIntervalHours
		}
	}
	premiumBySymbol := make(map[string]premiumIndex, len(premiums))
	for _, item := range premiums {
		premiumBySymbol[item.Symbol] = item
	}
	bookBySymbol := make(map[string]bookTicker, len(books))
	for _, item := range books {
		bookBySymbol[item.Symbol] = item
	}

	snapshots := make(map[string]venue.MarketData)
	for symbol, asset := range assets {
		premium, premiumOK := premiumBySymbol[symbol]
		book, bookOK := bookBySymbol[symbol]
		interval := intervals[symbol]
		if !premiumOK || !bookOK || interval <= 0 {
			continue
		}
		mark, markOK := positiveDecimal(premium.MarkPrice)
		index, indexOK := positiveDecimal(premium.IndexPrice)
		fundingRate, fundingOK := finiteDecimal(premium.LastFundingRate)
		bid, bidOK := positiveDecimal(book.BidPrice)
		bidQty, bidQtyOK := positiveDecimal(book.BidQty)
		ask, askOK := positiveDecimal(book.AskPrice)
		askQty, askQtyOK := positiveDecimal(book.AskQty)
		if !markOK || !indexOK || !fundingOK || !bidOK || !bidQtyOK || !askOK || !askQtyOK || ask < bid {
			continue
		}
		timestamp := minPositiveTimestamp(premium.Time, book.Time)
		if timestamp == 0 {
			continue
		}
		snapshots[symbol] = venue.MarketData{
			Venue: venueName, Asset: asset, MarketKey: symbol,
			MarkPrice: mark, IndexPrice: index, FundingRate: fundingRate / float64(interval),
			BidPrice: bid, BidSize: bid * bidQty, AskPrice: ask, AskSize: ask * askQty,
			Timestamp: time.UnixMilli(timestamp).UTC(),
		}
	}
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("Aster market data contains no complete USDT perpetual snapshots")
	}
	return snapshots, nil
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

func minPositiveTimestamp(left, right int64) int64 {
	if left <= 0 || right <= 0 {
		return 0
	}
	if left < right {
		return left
	}
	return right
}
