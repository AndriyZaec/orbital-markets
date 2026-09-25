package venue

import (
	"context"
	"time"
)

type HealthStatus string

const (
	HealthAvailable   HealthStatus = "available"
	HealthUnavailable HealthStatus = "unavailable"

	HealthReasonNotObserved        = "not_observed"
	HealthReasonDisconnected       = "disconnected"
	HealthReasonFetchFailed        = "fetch_failed"
	HealthReasonInvalidValue       = "invalid_value"
	HealthReasonIncompleteSnapshot = "incomplete_snapshot"
)

type ComponentHealth struct {
	Status           HealthStatus `json:"status"`
	SourceTime       time.Time    `json:"source_time,omitempty"`
	ReceivedAt       time.Time    `json:"received_at,omitempty"`
	SourceGeneration uint64       `json:"source_generation,omitempty"`
	Reason           string       `json:"reason,omitempty"`
}

type SourceHealth struct {
	Status     HealthStatus `json:"status"`
	Generation uint64       `json:"generation,omitempty"`
	ReceivedAt time.Time    `json:"received_at,omitempty"`
	Reason     string       `json:"reason,omitempty"`
}

type MarketHealth struct {
	Mark         ComponentHealth `json:"mark"`
	Index        ComponentHealth `json:"index"`
	Funding      ComponentHealth `json:"funding"`
	Book         ComponentHealth `json:"book"`
	OpenInterest ComponentHealth `json:"open_interest"`
	REST         SourceHealth    `json:"rest"`
	Stream       SourceHealth    `json:"stream"`
}

// MarketData is a normalized snapshot from a single venue for a single asset.
type MarketData struct {
	Venue        string        `json:"venue"`
	Asset        string        `json:"asset"`
	MarketKey    string        `json:"market_key"`
	MarkPrice    float64       `json:"mark_price"`
	IndexPrice   float64       `json:"index_price"`
	FundingRate  float64       `json:"funding_rate"`
	BidPrice     float64       `json:"bid_price"`
	BidSize      float64       `json:"bid_size"`
	AskPrice     float64       `json:"ask_price"`
	AskSize      float64       `json:"ask_size"`
	OpenInterest float64       `json:"open_interest"`
	MaxLeverage  int           `json:"max_leverage"`
	Timestamp    time.Time     `json:"timestamp"`
	Health       *MarketHealth `json:"health,omitempty"`
}

// FundingPayment is a realized account balance change from a venue funding settlement.
type FundingPayment struct {
	ExternalID string
	Venue      string
	Account    string
	Asset      string
	MarketKey  string
	AmountUSD  float64
	PaidAt     time.Time
}

// FundingHistory reads realized funding settlements for an account.
type FundingHistory interface {
	FundingPayments(ctx context.Context, account, asset string, since, until time.Time) ([]FundingPayment, error)
}

// Adapter is the interface every venue integration must implement.
type Adapter interface {
	Name() string
	FetchMarketData(ctx context.Context) ([]MarketData, error)
}

// MetadataRefresher is implemented by adapters whose execution constraints are
// not continuously refreshed with market data. Scanner plan construction uses
// it before reading fresh snapshots.
type MetadataRefresher interface {
	RefreshMetadata(ctx context.Context) error
}
