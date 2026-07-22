package api

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/db/sqlc"
	_ "modernc.org/sqlite"
)

// History benchmarks run against an explicitly supplied read-only DB so the
// benchmark can use a production-sized local snapshot without making normal
// tests depend on local data.
func BenchmarkHistory24hRaw(b *testing.B) {
	benchmarkHistory(b, sourceRaw, 24*time.Hour)
}

func BenchmarkHistory24h5m(b *testing.B) {
	benchmarkHistory(b, source5m, 24*time.Hour)
}

func BenchmarkHistory7d5m(b *testing.B) {
	benchmarkHistory(b, source5m, 7*24*time.Hour)
}

func BenchmarkHistory30d5m(b *testing.B) {
	benchmarkHistory(b, source5m, 30*24*time.Hour)
}

func BenchmarkHistory30d1h(b *testing.B) {
	benchmarkHistory(b, source1h, 30*24*time.Hour)
}

func BenchmarkHistory7d1h(b *testing.B) {
	benchmarkHistory(b, source1h, 7*24*time.Hour)
}

func benchmarkHistory(b *testing.B, source historySource, window time.Duration) {
	b.Helper()
	path := os.Getenv("ORBITAL_BENCH_DB")
	if path == "" {
		b.Skip("set ORBITAL_BENCH_DB to a local SQLite snapshot")
	}
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	q := sqlc.New(database)
	end := time.Now().UTC()
	start := end.Add(-window)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rowsA, err := fetchHistoryRows(context.Background(), q, source, "pacifica", "SOL", start.Unix(), end.Unix())
		if err != nil {
			b.Fatal(err)
		}
		rowsB, err := fetchHistoryRows(context.Background(), q, source, "hyperliquid", "SOL", start.Unix(), end.Unix())
		if err != nil {
			b.Fatal(err)
		}
		_ = pairHistoryRows(rowsA, rowsB, source)
	}
}
