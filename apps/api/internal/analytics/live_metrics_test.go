package analytics

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestLoadLiveMetricsCalculatesGrossAndHedgedVolume(t *testing.T) {
	db, err := sql.Open("sqlite", "file:analytics-metrics?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
CREATE TABLE live_positions (id TEXT PRIMARY KEY, asset TEXT, state TEXT, opened_at TEXT);
CREATE TABLE live_fills (
  position_id TEXT, leg INTEGER, venue TEXT, filled_amount REAL,
  avg_fill_price REAL, filled INTEGER, filled_at TEXT
);
CREATE TABLE live_close_outcomes (
  position_id TEXT, leg INTEGER, venue TEXT, filled_amount REAL,
  avg_fill_price REAL, resolved INTEGER, updated_at TEXT
);`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`
INSERT INTO live_positions VALUES ('p1', 'SOL', 'closed', '2026-01-01T00:00:00Z');
INSERT INTO live_positions VALUES ('p2', 'BTC', 'failed', NULL);
INSERT INTO live_fills VALUES
  ('p1', 1, 'pacifica', 2, 100, 1, '2026-01-01T00:00:00Z'),
  ('p1', 2, 'hyperliquid', 2, 101, 1, '2026-01-01T00:00:00Z'),
  ('p2', 1, 'pacifica', 1, 50, 1, '2026-01-01T00:00:00Z');
INSERT INTO live_close_outcomes VALUES
  ('p1', 1, 'pacifica', 2, 102, 1, '2026-01-02T00:00:00Z'),
  ('p1', 2, 'hyperliquid', 2, 103, 1, '2026-01-02T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}

	metrics, err := LoadLiveMetrics(context.Background(), db, time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Volume.AllTime.GrossVenueVolume != 862 {
		t.Fatalf("gross volume = %v, want 862", metrics.Volume.AllTime.GrossVenueVolume)
	}
	if metrics.Volume.AllTime.HedgedTradeVolume != 404 {
		t.Fatalf("hedged volume = %v, want 404", metrics.Volume.AllTime.HedgedTradeVolume)
	}
	if metrics.Volume.AllTime.OpenVolume != 452 || metrics.Volume.AllTime.CloseVolume != 410 {
		t.Fatalf("open/close volume = %v/%v, want 452/410", metrics.Volume.AllTime.OpenVolume, metrics.Volume.AllTime.CloseVolume)
	}
	if metrics.Trades.SuccessfulOpens != 1 || metrics.Trades.FailedOpens != 1 || metrics.Trades.ClosedTrades != 1 {
		t.Fatalf("unexpected trade metrics: %+v", metrics.Trades)
	}
}

func TestNotionalBucket(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{0, "under_1k"},
		{1_000, "1k_10k"},
		{10_000, "10k_100k"},
		{100_000, "100k_plus"},
	}
	for _, test := range tests {
		if got := NotionalBucket(test.value); got != test.want {
			t.Errorf("NotionalBucket(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}
