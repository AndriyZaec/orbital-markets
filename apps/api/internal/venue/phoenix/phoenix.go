package phoenix

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const venueName = "phoenix"

const (
	marketSnapshotMaxAge = 30 * time.Second
	marketClockSkew      = 5 * time.Second
)

// Adapter publishes Phoenix public market state to the scanner.
type Adapter struct {
	stream *MarketStream
	logger *slog.Logger
	now    func() time.Time
}

func New(logger *slog.Logger) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		stream: NewMarketStream(NewClient(DefaultRESTURL, nil), logger),
		logger: logger,
		now:    time.Now,
	}
}

func (a *Adapter) Name() string { return venueName }

func (a *Adapter) MarketDataUpdates() <-chan struct{} { return a.stream.Updates() }

// Snapshot shares the existing market stream with read-only account risk views.
func (a *Adapter) Snapshot() MarketStreamSnapshot { return a.stream.Snapshot() }

func (a *Adapter) Run(ctx context.Context) {
	if err := a.stream.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		a.logger.Error("phoenix market stream stopped", "err", err)
	}
}

func (a *Adapter) InactiveAssets() []string {
	snapshot := a.stream.Snapshot()
	assets := make(map[string]struct{})
	for _, market := range snapshot.Metadata.Markets {
		if market.Status != "active" {
			assets[market.Asset] = struct{}{}
		}
	}
	result := make([]string, 0, len(assets))
	for asset := range assets {
		result = append(result, asset)
	}
	sort.Strings(result)
	return result
}

func (a *Adapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	snapshot := a.stream.Snapshot()
	now := a.now()
	result := make([]venue.MarketData, 0, len(snapshot.Metadata.Markets))
	for _, market := range snapshot.Metadata.Markets {
		if market.Status != "active" {
			continue
		}
		stats, statsOK := snapshot.Stats[market.Symbol]
		book, bookOK := snapshot.Books[market.Symbol]
		if !statsOK || !bookOK {
			continue
		}
		if !marketSnapshotFresh(stats.SourceTime, now) || !marketSnapshotFresh(book.ReceivedAt, now) {
			continue
		}
		if snapshot.Connected && snapshot.Synchronized &&
			(stats.Generation != snapshot.Generation || book.Generation != snapshot.Generation) {
			continue
		}
		result = append(result, venue.MarketData{
			Venue: venueName, Asset: market.Asset, MarketKey: market.Symbol,
			MarkPrice: stats.MarkPrice, IndexPrice: stats.IndexPrice, FundingRate: stats.HourlyFundingRate,
			BidPrice: book.BidPrice, BidSize: book.BidSizeUSD,
			AskPrice: book.AskPrice, AskSize: book.AskSizeUSD,
			OpenInterest: stats.OpenInterest, MaxLeverage: market.MaxLeverage,
			Timestamp: olderTime(stats.SourceTime, book.ReceivedAt),
			Health:    marketHealth(snapshot, stats, book),
		})
	}
	return result, nil
}

func marketSnapshotFresh(timestamp, now time.Time) bool {
	return !timestamp.IsZero() && now.Sub(timestamp) <= marketSnapshotMaxAge &&
		!timestamp.After(now.Add(marketClockSkew))
}

func marketHealth(snapshot MarketStreamSnapshot, stats LiveMarketStats, book LiveBook) *venue.MarketHealth {
	statsHealth := venue.ComponentHealth{
		Status: venue.HealthAvailable, SourceTime: stats.SourceTime, ReceivedAt: stats.ReceivedAt,
		SourceGeneration: stats.Generation,
	}
	bookHealth := venue.ComponentHealth{
		Status: venue.HealthAvailable, ReceivedAt: book.ReceivedAt, SourceGeneration: book.Generation,
	}
	rest := venue.SourceHealth{Status: venue.HealthUnavailable, Reason: venue.HealthReasonNotObserved}
	if snapshot.MetadataGeneration > 0 {
		rest = venue.SourceHealth{
			Status: venue.HealthAvailable, Generation: snapshot.MetadataGeneration,
			ReceivedAt: snapshot.MetadataReceivedAt,
		}
	}
	stream := venue.SourceHealth{
		Status: venue.HealthUnavailable, Generation: snapshot.Generation,
		ReceivedAt: snapshot.LastReceivedAt, Reason: venue.HealthReasonNotObserved,
	}
	switch {
	case !snapshot.Connected:
		if snapshot.Generation > 0 {
			stream.Reason = venue.HealthReasonDisconnected
		}
	case !snapshot.Synchronized:
		stream.Reason = venue.HealthReasonIncompleteSnapshot
	default:
		stream.Status = venue.HealthAvailable
		stream.Reason = ""
	}
	return &venue.MarketHealth{
		Mark: statsHealth, Index: statsHealth, Funding: statsHealth,
		Book: bookHealth, OpenInterest: statsHealth, REST: rest, Stream: stream,
	}
}

func olderTime(left, right time.Time) time.Time {
	if left.IsZero() || (!right.IsZero() && right.Before(left)) {
		return right
	}
	return left
}
