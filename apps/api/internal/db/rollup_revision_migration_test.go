package db

import "testing"

func TestHourlySnapshotRevisionTracksContentChanges(t *testing.T) {
	database := openDatabaseAtMigration(t, 21)
	assertRevision := func(want int64) {
		t.Helper()
		var got int64
		if err := database.QueryRow(`SELECT revision FROM rollup_revisions WHERE name = 'market_snapshots_1h'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("revision = %d, want %d", got, want)
		}
	}
	assertRevision(0)
	if _, err := database.Exec(`INSERT INTO market_snapshots_1h (
		venue, asset, bucket_unix, open, high, low, close, funding_avg, oi_avg, bid_avg, ask_avg
	) VALUES ('aster', 'SOL', 1, 1, 1, 1, 1, 0.001, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	assertRevision(1)
	if _, err := database.Exec(`UPDATE market_snapshots_1h SET funding_avg = 0.002
		WHERE venue = 'aster' AND asset = 'SOL' AND bucket_unix = 1`); err != nil {
		t.Fatal(err)
	}
	assertRevision(2)
	if _, err := database.Exec(`UPDATE market_snapshots_1h SET funding_avg = 0.002
		WHERE venue = 'aster' AND asset = 'SOL' AND bucket_unix = 1`); err != nil {
		t.Fatal(err)
	}
	assertRevision(2)
	if _, err := database.Exec(`DELETE FROM market_snapshots_1h
		WHERE venue = 'aster' AND asset = 'SOL' AND bucket_unix = 1`); err != nil {
		t.Fatal(err)
	}
	assertRevision(3)
}
