package executor_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestDurableSessionLifecycleRetainsTerminalAuditRecord(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	expiresAt := time.Now().Add(3 * time.Minute).UTC().Truncate(time.Second)

	err = store.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: "session-1", State: "awaiting_leg1_signs", Payload: []byte(`{"plan":"one"}`),
		HasExposure: false, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: "session-1", State: "awaiting_leg2_sign", Payload: []byte(`{"plan":"one","armed":true}`),
		HasExposure: true, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	records, err := store.ListActiveDurableSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].State != "awaiting_leg2_sign" || !records[0].HasExposure {
		t.Fatalf("active records = %+v, want one exposed leg-2 session", records)
	}
	if string(records[0].Payload) != `{"plan":"one","armed":true}` {
		t.Fatalf("payload = %s", records[0].Payload)
	}

	if err := store.FinishDurableSession(ctx, "session-1", "recovered_degraded", "unwind not confirmed"); err != nil {
		t.Fatal(err)
	}
	records, err = store.ListActiveDurableSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("active records = %+v, want none after finish", records)
	}

	var state, detail string
	var terminalAt *string
	if err := database.QueryRow(`
		SELECT state, recovery_detail, terminal_at FROM live_sessions WHERE id = 'session-1'`,
	).Scan(&state, &detail, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if state != "recovered_degraded" || detail != "unwind not confirmed" || terminalAt == nil {
		t.Fatalf("terminal row = state %q detail %q at %v", state, detail, terminalAt)
	}
}

func TestPersistFullResultAtomicWritesCompleteTerminalRecord(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "result.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	result := &executor.ExecutionResult{
		PlanID: "plan-1", OpportunityID: "opportunity-1", Asset: "SOL",
		State: executor.ExecStateOpen, StartedAt: time.Now(),
		Leg1: executor.LegResult{
			Venue: "pacifica", Symbol: "SOL", Side: "long", Submitted: true,
			Accepted: true, Filled: true, RequestedAmt: 10, FilledAmount: 10, FillRatio: 1,
		},
		Leg2: executor.LegResult{
			Venue: "hyperliquid", Symbol: "SOL", Side: "short", Submitted: true,
			Accepted: true, Filled: true, RequestedAmt: 10, FilledAmount: 10, FillRatio: 1,
		},
	}
	if err := store.PersistFullResultAtomic(context.Background(), result, "pacifica", "hyperliquid", 10, 2); err != nil {
		t.Fatal(err)
	}

	for table, want := range map[string]int{"live_positions": 1, "live_fills": 2, "live_events": 5} {
		var got int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
}

func TestFlagDurableSessionKeepsPossibleExposureActive(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "blocked.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := store.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: "blocked-session", State: "leg1_submitted", Payload: []byte(`not-json`),
		AccountPacifica: "wallet-1", AccountHyperliquid: "0x1", Asset: "SOL",
		HasExposure: true, ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE live_sessions SET expires_at = 'invalid' WHERE id = 'blocked-session'`); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: "healthy-session", State: "awaiting_leg1_signs", Payload: []byte(`{}`),
		AccountPacifica: "wallet-2", AccountHyperliquid: "0x2", Asset: "BTC",
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FlagDurableSession(ctx, "blocked-session", "recovery_blocked", "invalid payload"); err != nil {
		t.Fatal(err)
	}
	records, err := store.ListActiveDurableSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("active records = %+v, want corrupt and healthy rows", records)
	}
	if records[0].ID != "blocked-session" || records[0].State != "recovery_blocked" || records[0].DecodeError == "" {
		t.Fatalf("blocked record = %+v, want retained row with decode error", records[0])
	}
	if records[1].ID != "healthy-session" || records[1].DecodeError != "" {
		t.Fatalf("healthy record = %+v, corrupt row blocked another session", records[1])
	}
	if err := store.UpsertRecoveryBlockedPosition(ctx, "blocked-session", "SOL", "invalid payload"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM live_positions WHERE id = 'recovery-blocked-session'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(executor.ExecStateDegraded) {
		t.Fatalf("operator-visible recovery position state = %q, want degraded", state)
	}
}

func TestDurableSessionsAllowOnlyOneActiveSessionPerAccountAsset(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "unique.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	record := executor.DurableSessionRecord{
		ID: "session-1", State: "awaiting_leg1_signs", Payload: []byte(`{}`),
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet", Asset: "SOL",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := store.UpsertDurableSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	record.ID = "session-2"
	if err := store.UpsertDurableSession(ctx, record); err == nil {
		t.Fatal("second active session for the same account and asset was accepted")
	}
	if err := store.FinishDurableSession(ctx, "session-1", "expired_safe", "done"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDurableSession(ctx, record); err != nil {
		t.Fatalf("new session after terminal predecessor: %v", err)
	}
}

func TestDurableSessionRecoveryLeasePreventsOverlappingOwners(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := store.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: "session-1", State: "leg1_submitting", Payload: []byte(`{}`),
		AccountPacifica: "wallet", AccountHyperliquid: "0xwallet", Asset: "SOL",
		HasExposure: true, ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDurableSession(ctx, "session-1", "server-1", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v; want true, nil", claimed, err)
	}
	claimed, err = store.ClaimDurableSession(ctx, "session-1", "server-2", time.Minute)
	if err != nil || claimed {
		t.Fatalf("overlapping claim = %v, %v; want false, nil", claimed, err)
	}
	if _, err := database.Exec(`UPDATE live_sessions SET recovery_lease_until = '2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimDurableSession(ctx, "session-1", "server-2", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim after lease expiry = %v, %v; want true, nil", claimed, err)
	}
	if err := store.FinishDurableSessionOwned(ctx, "session-1", "server-1", "failed", "stale owner"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale owner finish error = %v, want sql.ErrNoRows", err)
	}
	if err := store.FinishDurableSessionOwned(ctx, "session-1", "server-2", "recovered", "owner finished"); err != nil {
		t.Fatal(err)
	}
}
