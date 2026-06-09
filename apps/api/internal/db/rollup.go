package db

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/db/sqlc"
)

const (
	rollupInterval = 5 * time.Minute
	bucket5m       = int64(5 * 60)
	bucket1h       = int64(60 * 60)
	retain5m       = 30 * 24 * time.Hour
	retain1h       = 365 * 24 * time.Hour
)

// Rollup aggregates raw market_snapshots into 5-minute and hourly buckets.
//
// Every 5 minutes it folds the just-closed 5m bucket. On the top of an hour it
// also folds the just-closed 1h bucket from the 5m table. Retention is enforced
// inline: 30d for 5m, 1y for 1h.
type Rollup struct {
	db      *sql.DB
	queries *sqlc.Queries
	logger  *slog.Logger
}

func NewRollup(db *sql.DB, logger *slog.Logger) *Rollup {
	return &Rollup{db: db, queries: sqlc.New(db), logger: logger}
}

func (r *Rollup) Run(ctx context.Context) {
	ticker := time.NewTicker(rollupInterval)
	defer ticker.Stop()

	r.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Rollup) tick(ctx context.Context) {
	now := time.Now().Unix()

	prev5m := ((now - bucket5m) / bucket5m) * bucket5m
	if err := r.foldRawTo5m(ctx, prev5m); err != nil {
		r.logger.Error("rollup: raw→5m", "bucket", prev5m, "err", err)
	}

	// Once the just-closed 5m bucket sits at the top of an hour, that hour is
	// complete in the 5m table — fold it into 1h.
	if prev5m%bucket1h == bucket1h-bucket5m {
		prev1h := prev5m - (bucket1h - bucket5m)
		if err := r.fold5mTo1h(ctx, prev1h); err != nil {
			r.logger.Error("rollup: 5m→1h", "bucket", prev1h, "err", err)
		}
	}

	if err := r.retain(ctx, now); err != nil {
		r.logger.Error("rollup: retention", "err", err)
	}
}

// foldRawTo5m aggregates raw rows in [bucket, bucket+5m) into market_snapshots_5m.
func (r *Rollup) foldRawTo5m(ctx context.Context, bucket int64) error {
	rows, err := r.db.QueryContext(ctx,
		`SELECT venue, asset, mark_price, funding_rate, open_interest, bid_price, ask_price
		 FROM market_snapshots
		 WHERE ts_unix >= ? AND ts_unix < ?
		 ORDER BY venue, asset, ts_unix`,
		bucket, bucket+bucket5m,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	type acc struct {
		open, high, low, close                float64
		fundingSum, oiSum, bidSum, askSum     float64
		n                                     int
	}
	groups := map[[2]string]*acc{}

	for rows.Next() {
		var venue, asset string
		var mark, funding, oi, bid, ask float64
		if err := rows.Scan(&venue, &asset, &mark, &funding, &oi, &bid, &ask); err != nil {
			return err
		}
		key := [2]string{venue, asset}
		g, ok := groups[key]
		if !ok {
			g = &acc{open: mark, high: mark, low: mark, close: mark}
			groups[key] = g
		}
		if mark > g.high {
			g.high = mark
		}
		if mark < g.low {
			g.low = mark
		}
		g.close = mark
		g.fundingSum += funding
		g.oiSum += oi
		g.bidSum += bid
		g.askSum += ask
		g.n++
	}
	if err := rows.Err(); err != nil {
		return err
	}

	written := 0
	for key, g := range groups {
		n := float64(g.n)
		err := r.queries.UpsertSnapshot5m(ctx, sqlc.UpsertSnapshot5mParams{
			Venue:      key[0],
			Asset:      key[1],
			BucketUnix: bucket,
			Open:       g.open,
			High:       g.high,
			Low:        g.low,
			Close:      g.close,
			FundingAvg: g.fundingSum / n,
			OiAvg:      g.oiSum / n,
			BidAvg:     g.bidSum / n,
			AskAvg:     g.askSum / n,
		})
		if err != nil {
			return err
		}
		written++
	}
	if written > 0 {
		r.logger.Info("rollup 5m", "bucket", time.Unix(bucket, 0).UTC().Format(time.RFC3339), "rows", written)
	}
	return nil
}

// fold5mTo1h aggregates market_snapshots_5m rows in [bucket, bucket+1h) into market_snapshots_1h.
func (r *Rollup) fold5mTo1h(ctx context.Context, bucket int64) error {
	rows, err := r.db.QueryContext(ctx,
		`SELECT venue, asset, open, high, low, close, funding_avg, oi_avg, bid_avg, ask_avg
		 FROM market_snapshots_5m
		 WHERE bucket_unix >= ? AND bucket_unix < ?
		 ORDER BY venue, asset, bucket_unix`,
		bucket, bucket+bucket1h,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	type acc struct {
		open, high, low, close            float64
		fundingSum, oiSum, bidSum, askSum float64
		n                                 int
	}
	groups := map[[2]string]*acc{}

	for rows.Next() {
		var venue, asset string
		var open, high, low, cls, fAvg, oAvg, bAvg, aAvg float64
		if err := rows.Scan(&venue, &asset, &open, &high, &low, &cls, &fAvg, &oAvg, &bAvg, &aAvg); err != nil {
			return err
		}
		key := [2]string{venue, asset}
		g, ok := groups[key]
		if !ok {
			g = &acc{open: open, high: high, low: low, close: cls}
			groups[key] = g
		}
		if high > g.high {
			g.high = high
		}
		if low < g.low {
			g.low = low
		}
		g.close = cls
		g.fundingSum += fAvg
		g.oiSum += oAvg
		g.bidSum += bAvg
		g.askSum += aAvg
		g.n++
	}
	if err := rows.Err(); err != nil {
		return err
	}

	written := 0
	for key, g := range groups {
		n := float64(g.n)
		err := r.queries.UpsertSnapshot1h(ctx, sqlc.UpsertSnapshot1hParams{
			Venue:      key[0],
			Asset:      key[1],
			BucketUnix: bucket,
			Open:       g.open,
			High:       g.high,
			Low:        g.low,
			Close:      g.close,
			FundingAvg: g.fundingSum / n,
			OiAvg:      g.oiSum / n,
			BidAvg:     g.bidSum / n,
			AskAvg:     g.askSum / n,
		})
		if err != nil {
			return err
		}
		written++
	}
	if written > 0 {
		r.logger.Info("rollup 1h", "bucket", time.Unix(bucket, 0).UTC().Format(time.RFC3339), "rows", written)
	}
	return nil
}

func (r *Rollup) retain(ctx context.Context, now int64) error {
	cutoff5m := now - int64(retain5m.Seconds())
	cutoff1h := now - int64(retain1h.Seconds())

	res5m, err := r.db.ExecContext(ctx, "DELETE FROM market_snapshots_5m WHERE bucket_unix < ?", cutoff5m)
	if err != nil {
		return err
	}
	res1h, err := r.db.ExecContext(ctx, "DELETE FROM market_snapshots_1h WHERE bucket_unix < ?", cutoff1h)
	if err != nil {
		return err
	}
	d5m, _ := res5m.RowsAffected()
	d1h, _ := res1h.RowsAffected()
	if d5m > 0 || d1h > 0 {
		r.logger.Info("rollup retention", "deleted_5m", d5m, "deleted_1h", d1h)
	}
	return nil
}
