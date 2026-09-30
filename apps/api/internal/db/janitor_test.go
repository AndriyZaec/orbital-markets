package db

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestJanitorDeletesExpiredSnapshotsInBoundedBatches(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "janitor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for _, timestamp := range []int64{1, 2, 3, 100} {
		if _, err := database.Exec(`
			INSERT INTO market_snapshots (
				venue, asset, market_key, mark_price, index_price, funding_rate,
				bid_price, ask_price, open_interest, ts_unix
			) VALUES ('venue', ?, ?, 1, 1, 0, 1, 1, 1, ?)`, timestamp, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}

	janitor := NewJanitor(database, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	deleted, err := janitor.deleteExpiredSnapshotBatch(context.Background(), 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want bounded batch of 2", deleted)
	}

	var expired, fresh int
	if err := database.QueryRow("SELECT COUNT(*) FROM market_snapshots WHERE ts_unix < 50").Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM market_snapshots WHERE ts_unix >= 50").Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	if expired != 1 || fresh != 1 {
		t.Fatalf("remaining expired = %d, fresh = %d; want 1 and 1", expired, fresh)
	}
}

func TestSnapshotRetentionUsesTimestampIndex(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "janitor-index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	rows, err := database.Query("EXPLAIN QUERY PLAN SELECT id FROM market_snapshots WHERE ts_unix < ? ORDER BY ts_unix LIMIT ?", 50, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	foundIndex := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "idx_snapshots_ts") {
			foundIndex = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundIndex {
		t.Fatal("snapshot retention query does not use idx_snapshots_ts")
	}
}
