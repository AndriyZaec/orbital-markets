package db

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/db/sqlc"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const recordInterval = 1 * time.Minute

// MarketDataProvider exposes current market snapshots.
type MarketDataProvider interface {
	MarketData(ctx context.Context) []venue.MarketData
}

// Recorder periodically writes downsampled market snapshots to SQLite.
type Recorder struct {
	db       *sql.DB
	queries  *sqlc.Queries
	provider MarketDataProvider
	logger   *slog.Logger
}

func NewRecorder(db *sql.DB, provider MarketDataProvider, logger *slog.Logger) *Recorder {
	return &Recorder{
		db:       db,
		queries:  sqlc.New(db),
		provider: provider,
		logger:   logger,
	}
}

func (r *Recorder) Run(ctx context.Context) {
	ticker := time.NewTicker(recordInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.record(ctx)
		}
	}
}

func (r *Recorder) record(ctx context.Context) {
	data := r.provider.MarketData(ctx)
	if len(data) == 0 {
		return
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		r.logger.Error("record: begin tx", "err", err)
		return
	}
	defer tx.Rollback()

	qtx := r.queries.WithTx(tx)
	count := 0

	for _, md := range data {
		err := qtx.InsertSnapshot(ctx, sqlc.InsertSnapshotParams{
			Venue:        md.Venue,
			Asset:        md.Asset,
			MarketKey:    md.MarketKey,
			MarkPrice:    md.MarkPrice,
			IndexPrice:   md.IndexPrice,
			FundingRate:  md.FundingRate,
			BidPrice:     md.BidPrice,
			AskPrice:     md.AskPrice,
			OpenInterest: md.OpenInterest,
			Timestamp:    md.Timestamp.Format(time.RFC3339),
		})
		if err != nil {
			r.logger.Error("record snapshot", "venue", md.Venue, "asset", md.Asset, "err", err)
			continue
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		r.logger.Error("record: commit", "err", err)
		return
	}

	r.logger.Info("snapshots recorded", "count", count)
}
