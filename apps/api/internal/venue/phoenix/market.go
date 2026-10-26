package phoenix

import (
	"bytes"
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
	"time"
)

const (
	DefaultRESTURL   = "https://perp-api.phoenix.trade"
	requestTimeout   = 10 * time.Second
	maxResponseBytes = 4 * 1_024 * 1_024
)

type Client struct {
	baseURL string
	http    *http.Client
}

type ExchangeMetadata struct {
	Accounts ExchangeAccounts
	Markets  []MarketMetadata
}

type ExchangeAccounts struct {
	PerpAssetMap       string
	GlobalTraderIndex  []string
	ActiveTraderBuffer []string
}

type MarketMetadata struct {
	Symbol          string
	Asset           string
	Status          string
	MarketPubkey    string
	SplinePubkey    string
	AmountIncrement float64
	PriceIncrement  float64
	FundingInterval time.Duration
	MaxLeverage     int
	OpenInterest    float64
}

type exchangeResponse struct {
	Keys    exchangeKeys   `json:"keys"`
	Markets []marketConfig `json:"markets"`
}

type exchangeKeys struct {
	PerpAssetMap       string   `json:"perpAssetMap"`
	GlobalTraderIndex  []string `json:"globalTraderIndex"`
	ActiveTraderBuffer []string `json:"activeTraderBuffer"`
}

type marketConfig struct {
	Symbol                 string               `json:"symbol"`
	MarketStatus           string               `json:"marketStatus"`
	MarketPubkey           string               `json:"marketPubkey"`
	SplinePubkey           string               `json:"splinePubkey"`
	TickSize               int64                `json:"tickSize"`
	BaseLotsDecimals       int                  `json:"baseLotsDecimals"`
	FundingIntervalSeconds int64                `json:"fundingIntervalSeconds"`
	LeverageTiers          []leverageTier       `json:"leverageTiers"`
	StatsSnapshot          *marketStatsSnapshot `json:"statsSnapshot"`
}

type leverageTier struct {
	MaxLeverage float64 `json:"maxLeverage"`
}

type marketStatsSnapshot struct {
	OpenInterestBaseLots stringUint64 `json:"openInterestBaseLots"`
}

type stringUint64 uint64

func NewClient(baseURL string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: client}
}

func (c *Client) FetchExchangeMetadata(ctx context.Context) (ExchangeMetadata, error) {
	var response exchangeResponse
	if err := c.getJSON(ctx, "/v1/view/exchange", &response); err != nil {
		return ExchangeMetadata{}, err
	}
	if response.Keys.PerpAssetMap == "" || len(response.Keys.GlobalTraderIndex) == 0 ||
		len(response.Keys.ActiveTraderBuffer) == 0 {
		return ExchangeMetadata{}, errors.New("Phoenix exchange metadata is missing required accounts")
	}
	if len(response.Markets) == 0 {
		return ExchangeMetadata{}, errors.New("Phoenix exchange metadata contains no markets")
	}

	sort.Slice(response.Markets, func(i, j int) bool {
		return response.Markets[i].Symbol < response.Markets[j].Symbol
	})
	markets := make([]MarketMetadata, 0, len(response.Markets))
	for _, config := range response.Markets {
		market, err := normalizeMarketConfig(config)
		if err != nil {
			return ExchangeMetadata{}, fmt.Errorf("normalize Phoenix market %q: %w", config.Symbol, err)
		}
		markets = append(markets, market)
	}

	return ExchangeMetadata{
		Accounts: ExchangeAccounts{
			PerpAssetMap:       response.Keys.PerpAssetMap,
			GlobalTraderIndex:  append([]string(nil), response.Keys.GlobalTraderIndex...),
			ActiveTraderBuffer: append([]string(nil), response.Keys.ActiveTraderBuffer...),
		},
		Markets: markets,
	}, nil
}

func (c *Client) getJSON(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build Phoenix %s request: %w", path, err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("fetch Phoenix %s: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Phoenix %s returned HTTP %d", path, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Phoenix %s: %w", path, err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("Phoenix %s response exceeds %d bytes", path, maxResponseBytes)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode Phoenix %s: %w", path, err)
	}
	return nil
}

func normalizeMarketConfig(config marketConfig) (MarketMetadata, error) {
	if config.Symbol == "" || config.MarketPubkey == "" || config.SplinePubkey == "" {
		return MarketMetadata{}, errors.New("missing symbol or market accounts")
	}
	if !validMarketStatus(config.MarketStatus) {
		return MarketMetadata{}, fmt.Errorf("unknown market status %q", config.MarketStatus)
	}
	if config.TickSize <= 0 || config.FundingIntervalSeconds <= 0 {
		return MarketMetadata{}, errors.New("missing tick or funding configuration")
	}
	amountIncrement := math.Pow10(-config.BaseLotsDecimals)
	priceIncrement := float64(config.TickSize) * math.Pow10(config.BaseLotsDecimals) / 1_000_000
	if !positiveFinite(amountIncrement) || !positiveFinite(priceIncrement) {
		return MarketMetadata{}, errors.New("invalid amount or price increment")
	}
	maxLeverage := 0.0
	for _, tier := range config.LeverageTiers {
		if tier.MaxLeverage > maxLeverage {
			maxLeverage = tier.MaxLeverage
		}
	}
	if !positiveFinite(maxLeverage) || math.Floor(maxLeverage) < 1 {
		return MarketMetadata{}, errors.New("missing positive leverage tier")
	}

	openInterest := 0.0
	if config.StatsSnapshot != nil {
		openInterest = float64(config.StatsSnapshot.OpenInterestBaseLots) * amountIncrement
		if openInterest < 0 || math.IsNaN(openInterest) || math.IsInf(openInterest, 0) {
			return MarketMetadata{}, errors.New("invalid open interest")
		}
	}
	asset := strings.TrimSuffix(config.Symbol, "-PERP")
	if asset == "" {
		return MarketMetadata{}, errors.New("empty normalized asset")
	}
	return MarketMetadata{
		Symbol: config.Symbol, Asset: asset, Status: config.MarketStatus,
		MarketPubkey: config.MarketPubkey, SplinePubkey: config.SplinePubkey,
		AmountIncrement: amountIncrement,
		PriceIncrement:  priceIncrement,
		FundingInterval: time.Duration(config.FundingIntervalSeconds) * time.Second,
		MaxLeverage:     int(math.Floor(maxLeverage)), OpenInterest: openInterest,
	}, nil
}

func validMarketStatus(status string) bool {
	switch status {
	case "uninitialized", "active", "postOnly", "paused", "closed", "tombstoned":
		return true
	default:
		return false
	}
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (value *stringUint64) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 1 && data[0] == '"' && data[len(data)-1] == '"' {
		data = data[1 : len(data)-1]
	}
	parsed, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return err
	}
	*value = stringUint64(parsed)
	return nil
}
