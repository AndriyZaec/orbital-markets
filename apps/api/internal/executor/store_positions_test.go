package executor_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestListRecentActivePositionsForAccountsExcludesHistoryAndAppliesLimit(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "positions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	startedAt := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	states := []string{"open", "closed", "degraded", "failed", "closing", "pending"}
	for index, state := range states {
		id := state + "-position"
		timestamp := startedAt.Add(time.Duration(index) * time.Minute).Format(time.RFC3339Nano)
		if _, err := database.Exec(`
			INSERT INTO live_positions (
				id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
				notional, leverage, started_at, updated_at,
				account_pacifica, account_hyperliquid
			) VALUES (?, ?, 'opportunity', 'SOL', 'pacifica', 'hyperliquid', ?, 1000, 3, ?, ?, 'sol-owner', '0xevm')`,
			id, id, state, timestamp, timestamp,
		); err != nil {
			t.Fatal(err)
		}
	}

	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	positions, err := store.ListRecentActivePositionsForAccounts(
		context.Background(),
		" sol-owner ",
		" 0xEVM ",
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 2 || positions[0].State != "pending" || positions[1].State != "closing" {
		t.Fatalf("positions = %+v, want two newest active states", positions)
	}
}
