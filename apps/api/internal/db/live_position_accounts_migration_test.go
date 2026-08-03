package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestLivePositionAccountMigrationBackfillsDurableSessionOwner(t *testing.T) {
	database := openDatabaseAtMigration(t, 10)
	insertLegacyLivePosition(t, database, "plan-legacy")
	_, err := database.Exec(`
		INSERT INTO live_sessions (
			id, state, payload, account_pacifica, account_hyperliquid, asset,
			has_exposure, expires_at, created_at, updated_at
		) VALUES (
			'session-legacy', 'open', '{"plan":{"id":"plan-legacy"}}',
			'sol-wallet', '0xwallet', 'SOL', 0,
			'2026-08-03T13:00:00Z', '2026-08-03T12:00:00Z', '2026-08-03T12:01:00Z'
		)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		INSERT INTO live_sessions (
			id, state, payload, account_pacifica, account_hyperliquid, asset,
			has_exposure, expires_at, created_at, updated_at
		) VALUES (
			'malformed-session', 'recovery_blocked', 'not-json',
			'other-sol-wallet', '0xother', 'BTC', 1,
			'2026-08-03T13:00:00Z', '2026-08-03T12:00:00Z', '2026-08-03T12:01:00Z'
		)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 11); err != nil {
		t.Fatal(err)
	}
	var pacifica, hyperliquid string
	if err := database.QueryRow(`
		SELECT account_pacifica, account_hyperliquid FROM live_positions WHERE id = 'plan-legacy'
	`).Scan(&pacifica, &hyperliquid); err != nil {
		t.Fatal(err)
	}
	if pacifica != "sol-wallet" || hyperliquid != "0xwallet" {
		t.Fatalf("backfilled accounts = %q/%q", pacifica, hyperliquid)
	}
}

func TestLivePositionAccountMigrationAllowsUnownedLegacyPosition(t *testing.T) {
	database := openDatabaseAtMigration(t, 10)
	insertLegacyLivePosition(t, database, "unowned-plan")
	if err := goose.UpTo(database, "migrations", 11); err != nil {
		t.Fatal(err)
	}
	var pacifica, hyperliquid string
	if err := database.QueryRow(`
		SELECT account_pacifica, account_hyperliquid FROM live_positions WHERE id = 'unowned-plan'
	`).Scan(&pacifica, &hyperliquid); err != nil {
		t.Fatal(err)
	}
	if pacifica != "" || hyperliquid != "" {
		t.Fatalf("unowned legacy accounts = %q/%q, want empty", pacifica, hyperliquid)
	}
}

func openDatabaseAtMigration(t *testing.T, version int64) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db")+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", version); err != nil {
		t.Fatal(err)
	}
	return database
}

func insertLegacyLivePosition(t *testing.T, database *sql.DB, id string) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			notional, leverage, started_at, opened_at, completed_at, updated_at
		) VALUES (?, ?, 'opportunity', 'SOL', 'pacifica', 'hyperliquid', 'open',
			10, 2, '2026-08-03T12:00:00Z', '2026-08-03T12:01:00Z', NULL, '2026-08-03T12:01:00Z')`,
		id, id)
	if err != nil {
		t.Fatal(err)
	}
}
