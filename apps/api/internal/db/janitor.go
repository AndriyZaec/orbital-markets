package db

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"time"
)

const (
	janitorInterval  = 1 * time.Hour
	snapshotRetainer = 7 * 24 * time.Hour
)

// Janitor enforces SQLite retention and reclaims space.
//
// Hourly:
//   - DELETE market_snapshots older than snapshotRetainer
//   - PRAGMA incremental_vacuum to reclaim freed pages
//   - PRAGMA wal_checkpoint(TRUNCATE) to bound the WAL file
type Janitor struct {
	db     *sql.DB
	path   string
	logger *slog.Logger
}

func NewJanitor(db *sql.DB, path string, logger *slog.Logger) *Janitor {
	return &Janitor{db: db, path: path, logger: logger}
}

func (j *Janitor) Run(ctx context.Context) {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()

	// First pass shortly after startup so a fresh deploy reclaims promptly.
	j.sweep(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.sweep(ctx)
		}
	}
}

func (j *Janitor) sweep(ctx context.Context) {
	cutoff := time.Now().Add(-snapshotRetainer).Unix()

	res, err := j.db.ExecContext(ctx, "DELETE FROM market_snapshots WHERE ts_unix < ?", cutoff)
	if err != nil {
		j.logger.Error("janitor: delete snapshots", "err", err)
		return
	}
	deleted, _ := res.RowsAffected()

	if _, err := j.db.ExecContext(ctx, "PRAGMA incremental_vacuum(1000)"); err != nil {
		j.logger.Error("janitor: incremental_vacuum", "err", err)
	}

	if _, err := j.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		j.logger.Error("janitor: wal_checkpoint", "err", err)
	}

	j.logger.Info("janitor swept",
		"deleted", deleted,
		"cutoff", time.Unix(cutoff, 0).UTC().Format(time.RFC3339),
		"db_bytes", fileSize(j.path),
	)
}

func fileSize(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
