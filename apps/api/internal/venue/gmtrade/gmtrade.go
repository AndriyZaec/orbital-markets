package gmtrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const venueName = "gmtrade"

// sidecarEntry matches the JSON shape from the Rust sidecar.
type sidecarEntry struct {
	Venue        string  `json:"venue"`
	Asset        string  `json:"asset"`
	MarketKey    string  `json:"market_key"`
	MarkPrice    float64 `json:"mark_price"`
	IndexPrice   float64 `json:"index_price"`
	FundingRate  float64 `json:"funding_rate"`
	BidPrice     float64 `json:"bid_price"`
	AskPrice     float64 `json:"ask_price"`
	OpenInterest float64 `json:"open_interest"`
	Timestamp    string  `json:"timestamp"`
}

// Adapter polls the GMTrade Rust sidecar for market data.
type Adapter struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

func New(logger *slog.Logger, sidecarURL string) *Adapter {
	return &Adapter{
		baseURL: sidecarURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

func (a *Adapter) Name() string {
	return venueName
}

func (a *Adapter) FetchMarketData(ctx context.Context) ([]venue.MarketData, error) {
	url := a.baseURL + "/markets"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch sidecar: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sidecar returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var entries []sidecarEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	out := make([]venue.MarketData, 0, len(entries))
	for _, e := range entries {
		ts, _ := time.Parse(time.RFC3339, e.Timestamp)
		out = append(out, venue.MarketData{
			Venue:        e.Venue,
			Asset:        e.Asset,
			MarketKey:    e.MarketKey,
			MarkPrice:    e.MarkPrice,
			IndexPrice:   e.IndexPrice,
			FundingRate:  e.FundingRate,
			BidPrice:     e.BidPrice,
			AskPrice:     e.AskPrice,
			OpenInterest: e.OpenInterest,
			Timestamp:    ts,
		})
	}
	return out, nil
}
