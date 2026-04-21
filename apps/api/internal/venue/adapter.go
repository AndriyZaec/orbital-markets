package venue

import (
	"context"
	"time"
)

// MarketData is a normalized snapshot from a single venue for a single asset.
type MarketData struct {
	Venue        string    `json:"venue"`
	Asset        string    `json:"asset"`
	MarketKey    string    `json:"market_key"`
	MarkPrice    float64   `json:"mark_price"`
	IndexPrice   float64   `json:"index_price"`
	FundingRate  float64   `json:"funding_rate"`
	BidPrice     float64   `json:"bid_price"`
	BidSize      float64   `json:"bid_size"`
	AskPrice     float64   `json:"ask_price"`
	AskSize      float64   `json:"ask_size"`
	OpenInterest float64   `json:"open_interest"`
	Timestamp    time.Time `json:"timestamp"`
}

// Adapter is the interface every venue integration must implement.
type Adapter interface {
	Name() string
	FetchMarketData(ctx context.Context) ([]MarketData, error)
}
