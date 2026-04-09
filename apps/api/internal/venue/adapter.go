package venue

import "context"

// MarketData is a normalized snapshot from a single venue for a single asset.
type MarketData struct {
	Asset       string  `json:"asset"`
	Venue       string  `json:"venue"`
	MarkPrice   float64 `json:"mark_price"`
	IndexPrice  float64 `json:"index_price"`
	FundingRate float64 `json:"funding_rate"`
	OpenInterest float64 `json:"open_interest"`
	BidPrice    float64 `json:"bid_price"`
	AskPrice    float64 `json:"ask_price"`
}

// Adapter is the interface every venue integration must implement.
type Adapter interface {
	Name() string
	FetchMarketData(ctx context.Context, assets []string) ([]MarketData, error)
}
