package analytics

import (
	"context"
	"database/sql"
	"math"
	"strings"
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
		CREATE INDEX idx_snapshots_1h_weekly_apr
			ON market_snapshots_1h(bucket_unix, asset, venue, funding_avg);
	`); err != nil {
		t.Fatal(err)
	}
	assertWeeklyAPRUsesCoveringIndex(t, db)

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

func assertWeeklyAPRUsesCoveringIndex(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+weeklyAPRQuery, 0, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	details := strings.Join(plan, "\n")
	if !strings.Contains(details, "USING COVERING INDEX idx_snapshots_1h_weekly_apr") || strings.Contains(details, "USE TEMP B-TREE") {
		t.Fatalf("weekly APR query plan is not an ordered covering-index scan:\n%s", details)
	}
}

func TestLoadWeeklyAPRBuildsEveryPairFromHourlyVenueRows(t *testing.T) {
	db, err := sql.Open("sqlite", "file:weekly-apr-pairs?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE market_snapshots_1h (
			venue TEXT, asset TEXT, bucket_unix INTEGER, funding_avg REAL
		);
		CREATE INDEX idx_snapshots_1h_weekly_apr
			ON market_snapshots_1h(bucket_unix, asset, venue, funding_avg);
	`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	bucket := now.Add(-time.Hour).Unix()
	if _, err := db.Exec(`INSERT INTO market_snapshots_1h VALUES
		('aster', 'SOL', ?, -0.000003),
		('hyperliquid', 'SOL', ?, 0.000002),
		('pacifica', 'SOL', ?, 0.000010)`, bucket, bucket, bucket); err != nil {
		t.Fatal(err)
	}

	report, err := LoadWeeklyAPR(context.Background(), db, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	pairs := make(map[string]bool)
	for _, row := range report.Rows {
		pairs[row.VenueLong+"/"+row.VenueShort] = true
	}
	if len(report.Rows) != 3 || !pairs["aster/hyperliquid"] || !pairs["aster/pacifica"] || !pairs["hyperliquid/pacifica"] {
		t.Fatalf("rows = %+v, want all three venue pairs", report.Rows)
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
