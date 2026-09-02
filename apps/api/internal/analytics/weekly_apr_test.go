package analytics

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestLoadWeeklyAPRUsesPeakDirectionForSignedWeeklyAverage(t *testing.T) {
	db, err := sql.Open("sqlite", "file:weekly-apr?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE market_snapshots_1h (
			venue TEXT, asset TEXT, bucket_unix INTEGER, funding_avg REAL
		);
	`); err != nil {
		t.Fatal(err)
	}

	currentMonday := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	previousMonday := currentMonday.AddDate(0, 0, -7)
	insertFundingPair(t, db, "SOL", currentMonday.Add(time.Hour), 0.000010, 0.000002)
	insertFundingPair(t, db, "SOL", currentMonday.Add(2*time.Hour), 0.000001, 0.000005)
	insertFundingPair(t, db, "BTC", previousMonday.Add(time.Hour), -0.000006, 0.000003)

	report, err := LoadWeeklyAPR(context.Background(), db, time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC), 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(report.Rows))
	}

	sol := report.Rows[0]
	if sol.WeekStart != "2026-08-31" || sol.Ticker != "SOL" || sol.VenueLong != "hyperliquid" || sol.VenueShort != "pacifica" {
		t.Fatalf("unexpected SOL row: %+v", sol)
	}
	if math.Abs(sol.MaxAPR-0.07008) > 1e-9 || math.Abs(sol.WeeklyAverageAPR-0.01752) > 1e-9 {
		t.Fatalf("SOL APR = max %v average %v", sol.MaxAPR, sol.WeeklyAverageAPR)
	}

	btc := report.Rows[1]
	if btc.WeekStart != "2026-08-24" || btc.VenueLong != "pacifica" || btc.VenueShort != "hyperliquid" {
		t.Fatalf("unexpected BTC row: %+v", btc)
	}
	if math.Abs(btc.MaxAPR-0.07884) > 1e-9 || math.Abs(btc.WeeklyAverageAPR-0.07884) > 1e-9 {
		t.Fatalf("BTC APR = max %v average %v", btc.MaxAPR, btc.WeeklyAverageAPR)
	}
}

func insertFundingPair(t *testing.T, db *sql.DB, asset string, bucket time.Time, pacificaRate, hyperliquidRate float64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO market_snapshots_1h VALUES
		('pacifica', ?, ?, ?), ('hyperliquid', ?, ?, ?)`,
		asset, bucket.Unix(), pacificaRate, asset, bucket.Unix(), hyperliquidRate); err != nil {
		t.Fatal(err)
	}
}
