package db

import (
	"math"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestPhoenixFundingMigrationRepairsHistoryOnce(t *testing.T) {
	database := openDatabaseAtMigration(t, 26)
	rows := []struct {
		venue, asset string
		before, want float64
	}{
		{"phoenix", "MON", -0.011622, -0.00011622},
		{"phoenix", "BTC", 0.0013, 0.000013},
		{"phoenix", "ZERO", 0, 0},
		{"hyperliquid", "BTC", 0.000013, 0.000013},
	}
	for _, row := range rows {
		if _, err := database.Exec(`INSERT INTO market_snapshots
			(venue, asset, market_key, mark_price, index_price, funding_rate, bid_price, ask_price, open_interest, ts_unix)
			VALUES (?, ?, ?, 100, 100, ?, 99, 101, 10, 1)`, row.venue, row.asset, row.asset, row.before); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"market_snapshots_5m", "market_snapshots_1h"} {
			if _, err := database.Exec(`INSERT INTO `+table+`
				(venue, asset, bucket_unix, open, high, low, close, funding_avg, oi_avg, bid_avg, ask_avg)
				VALUES (?, ?, 1, 100, 100, 100, 100, ?, 10, 99, 101)`, row.venue, row.asset, row.before); err != nil {
				t.Fatal(err)
			}
		}
	}

	for run := 0; run < 2; run++ {
		if err := goose.UpTo(database, "migrations", 27); err != nil {
			t.Fatal(err)
		}
		for _, table := range []struct{ name, rate, price string }{
			{"market_snapshots", "funding_rate", "mark_price"},
			{"market_snapshots_5m", "funding_avg", "close"},
			{"market_snapshots_1h", "funding_avg", "close"},
		} {
			for _, row := range rows {
				var rate, price float64
				if err := database.QueryRow(`SELECT `+table.rate+`, `+table.price+` FROM `+table.name+` WHERE venue = ? AND asset = ?`, row.venue, row.asset).Scan(&rate, &price); err != nil {
					t.Fatal(err)
				}
				if math.Abs(rate-row.want) > 1e-15 || price != 100 {
					t.Fatalf("run %d %s %s/%s: rate=%g price=%g, want rate=%g price=100", run, table.name, row.venue, row.asset, rate, price, row.want)
				}
			}
		}
	}
}
