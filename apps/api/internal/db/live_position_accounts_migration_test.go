package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestLiveAccountBindingsMigrationBackfillsOwnedRecordsOnly(t *testing.T) {
	database := openDatabaseAtMigration(t, 16)
	_, err := database.Exec(`
		INSERT INTO live_sessions (
			id, state, payload, account_pacifica, account_hyperliquid, asset,
			has_exposure, expires_at, created_at, updated_at
		) VALUES (
			'session-owned', 'awaiting_leg1_signs', '{}', 'sol-wallet', '0xWaLlEt', 'SOL',
			0, '2026-08-03T13:00:00Z', '2026-08-03T12:00:00Z', '2026-08-03T12:01:00Z'
		)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_pacifica, account_hyperliquid,
			notional, leverage, started_at, updated_at
		) VALUES
			('position-owned', 'plan-owned', 'opportunity', 'SOL', 'pacifica', 'hyperliquid', 'open',
			 'sol-wallet', '0xWaLlEt', 10, 2, '2026-08-03T12:00:00Z', '2026-08-03T12:01:00Z'),
			('position-unowned', 'plan-unowned', 'opportunity', 'BTC', 'pacifica', 'hyperliquid', 'open',
			 '', '', 10, 2, '2026-08-03T12:00:00Z', '2026-08-03T12:01:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 17); err != nil {
		t.Fatal(err)
	}

	const want = `{"hyperliquid":"0xwallet","pacifica":"sol-wallet"}`
	for table, id := range map[string]string{
		"live_sessions":  "session-owned",
		"live_positions": "position-owned",
	} {
		var bindingsJSON, bindingsKey string
		if err := database.QueryRow(`SELECT account_bindings_json, account_bindings_key FROM `+table+` WHERE id = ?`, id).
			Scan(&bindingsJSON, &bindingsKey); err != nil {
			t.Fatal(err)
		}
		if bindingsJSON != want || bindingsKey != want {
			t.Fatalf("%s bindings = %q / %q, want %q", table, bindingsJSON, bindingsKey, want)
		}
	}

	var bindingsJSON, bindingsKey string
	if err := database.QueryRow(`
		SELECT account_bindings_json, account_bindings_key
		FROM live_positions WHERE id = 'position-unowned'`,
	).Scan(&bindingsJSON, &bindingsKey); err != nil {
		t.Fatal(err)
	}
	if bindingsJSON != "" || bindingsKey != "" {
		t.Fatalf("unowned bindings = %q / %q, want empty", bindingsJSON, bindingsKey)
	}
}

func TestLiveAccountBindingsMigrationPreventsRollingBinarySessionDuplicates(t *testing.T) {
	database := openDatabaseAtMigration(t, 17)
	_, err := database.Exec(`
		INSERT INTO live_sessions (
			id, state, payload, account_pacifica, account_hyperliquid,
			account_bindings_json, account_bindings_key, asset,
			has_exposure, expires_at, created_at, updated_at
		) VALUES (
			'new-binary', 'awaiting_leg1_signs', '{}', 'sol-wallet', '0xwallet',
			'{"hyperliquid":"0xwallet","pacifica":"sol-wallet"}',
			'{"hyperliquid":"0xwallet","pacifica":"sol-wallet"}',
			'SOL', 0, '2026-08-03T13:00:00Z', '2026-08-03T12:00:00Z', '2026-08-03T12:01:00Z'
		)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		INSERT INTO live_sessions (
			id, state, payload, account_pacifica, account_hyperliquid, asset,
			has_exposure, expires_at, created_at, updated_at
		) VALUES (
			'old-binary', 'awaiting_leg1_signs', '{}', 'sol-wallet', '0xWaLlEt', 'SOL',
			0, '2026-08-03T13:00:00Z', '2026-08-03T12:00:02Z', '2026-08-03T12:01:02Z'
		)`)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		t.Fatalf("old binary duplicate error = %v, want unique constraint", err)
	}
}

func TestFundingCoverageMigrationBackfillsAndCompactsLegacyPayments(t *testing.T) {
	database := openDatabaseAtMigration(t, 19)
	_, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			notional, leverage, started_at, opened_at, updated_at
		) VALUES
			('position-open', 'plan-open', 'opportunity', 'SOL', 'aster', 'hyperliquid', 'open',
			 10, 2, '2026-08-01T12:00:00Z', '2026-08-01T12:00:00Z', '2026-08-03T12:00:00Z'),
			('position-closed', 'plan-closed', 'opportunity', 'SOL', 'aster', 'hyperliquid', 'closed',
			 10, 2, '2026-08-01T12:00:00Z', '2026-08-01T12:00:00Z', '2026-08-03T12:00:00Z');
		INSERT INTO live_funding_sync (position_id, synced_at, finalized)
		VALUES ('position-closed', '2026-08-03T12:00:00Z', 1);
		INSERT INTO live_funding_payments (
			position_id, venue, account, external_id, asset, amount_usd, paid_at, created_at
		) VALUES
			('position-open', 'aster', '0xopen', 'open-payment', 'SOL', 1,
			 '2026-08-03T12:00:00.123Z', '2026-08-03T12:01:00Z'),
			('position-closed', 'aster', '0xclosed', 'closed-payment', 'SOL', 2,
			 '2026-08-03T12:00:00.456Z', '2026-08-03T12:01:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 20); err != nil {
		t.Fatal(err)
	}
	wantPaidAt, err := time.Parse(time.RFC3339Nano, "2026-08-03T12:00:00.123Z")
	if err != nil {
		t.Fatal(err)
	}
	var paidAtMS int64
	if err := database.QueryRow(`
		SELECT paid_at_ms FROM live_funding_payments WHERE position_id = 'position-open'
	`).Scan(&paidAtMS); err != nil {
		t.Fatal(err)
	}
	if paidAtMS != wantPaidAt.UnixMilli() {
		t.Fatalf("backfilled paid_at_ms = %d, want %d", paidAtMS, wantPaidAt.UnixMilli())
	}
	var finalizedRows int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM live_funding_payments WHERE position_id = 'position-closed'
	`).Scan(&finalizedRows); err != nil {
		t.Fatal(err)
	}
	if finalizedRows != 0 {
		t.Fatalf("finalized legacy payment rows = %d, want 0", finalizedRows)
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
