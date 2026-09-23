package executor_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"reflect"
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
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet",
		HasExposure: false, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: "session-1", State: "awaiting_leg2_sign", Payload: []byte(`{"plan":"one","armed":true}`),
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet",
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

func TestGetDurableSessionForPlanFindsTerminalSessionPayload(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "plan-session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	payload := []byte(`{"plan":{"id":"plan-2z"}}`)
	if err := store.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: "session-2z", State: "complete", Payload: payload, Asset: "2Z",
		AccountBindings: map[string]string{"aster": "0xowner", "pacifica": "sol-owner"},
		ExpiresAt:       time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishDurableSession(ctx, "session-2z", "complete", ""); err != nil {
		t.Fatal(err)
	}

	record, err := store.GetDurableSessionForPlan(ctx, "plan-2z")
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "session-2z" || !record.Terminal || string(record.Payload) != string(payload) {
		t.Fatalf("record = %+v", record)
	}
}

func TestDurableSessionsUseAnExactGenericBindingSlot(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "generic-sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bindings := map[string]string{"beta": "owner-b", "alpha": "owner-a"}
	record := executor.DurableSessionRecord{
		ID: "session-1", State: "awaiting_leg1_signs", Payload: []byte(`{}`),
		AccountBindings: bindings, Asset: "SOL", ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := store.UpsertDurableSession(ctx, record); err != nil {
		t.Fatal(err)
	}

	active, err := store.ActiveDurableSessionForBindings(ctx, map[string]string{
		"alpha": "owner-a", "beta": "owner-b",
	}, "SOL")
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != "session-1" || !reflect.DeepEqual(active.AccountBindings, bindings) {
		t.Fatalf("active session = %+v", active)
	}
	var legacyPacifica, legacyHyperliquid, bindingsKey string
	if err := database.QueryRow(`
		SELECT account_pacifica, account_hyperliquid, account_bindings_key
		FROM live_sessions WHERE id = 'session-1'`,
	).Scan(&legacyPacifica, &legacyHyperliquid, &bindingsKey); err != nil {
		t.Fatal(err)
	}
	if legacyPacifica != "" || legacyHyperliquid != "" || bindingsKey == "" {
		t.Fatalf("generic legacy columns/key = %q / %q / %q", legacyPacifica, legacyHyperliquid, bindingsKey)
	}

	record.ID = "session-duplicate"
	if err := store.UpsertDurableSession(ctx, record); err == nil {
		t.Fatal("duplicate active generic binding slot was accepted")
	}
	record.ID = "session-other"
	record.AccountBindings = map[string]string{"alpha": "owner-a", "beta": "other-owner"}
	if err := store.UpsertDurableSession(ctx, record); err != nil {
		t.Fatalf("different generic binding slot was rejected: %v", err)
	}
}

func TestDurableSessionRecoveryRetainsOwnershipFromCanonicalKey(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "generic-corrupt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bindings := map[string]string{"alpha": "owner-a", "beta": "owner-b"}
	if err := store.UpsertDurableSession(ctx, executor.DurableSessionRecord{
		ID: "corrupt-session", State: "leg1_submitted", Payload: []byte(`{}`),
		AccountBindings: bindings, Asset: "SOL", HasExposure: true,
		ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		UPDATE live_sessions SET account_bindings_json = 'not-json'
		WHERE id = 'corrupt-session'`); err != nil {
		t.Fatal(err)
	}
	records, err := store.ListActiveDurableSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].DecodeError == "" ||
		!reflect.DeepEqual(records[0].AccountBindings, bindings) {
		t.Fatalf("corrupt record = %+v, want decode error with recovered ownership", records)
	}
}

func TestGenericReadersFallBackToLegacyAccountColumns(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "legacy-read.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = database.Exec(`
		INSERT INTO live_sessions (
			id, state, payload, account_pacifica, account_hyperliquid, asset,
			has_exposure, expires_at, created_at, updated_at
		) VALUES ('legacy-session', 'awaiting_leg1_signs', '{}', 'sol-owner', '0xowner',
			'SOL', 0, ?, ?, ?)`, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_pacifica, account_hyperliquid, notional, leverage, started_at, updated_at
		) VALUES ('legacy-position', 'legacy-plan', 'opportunity', 'SOL', 'pacifica',
			'hyperliquid', 'open', 'sol-owner', '0xowner', 10, 2, ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}

	bindings := map[string]string{"pacifica": "sol-owner", "hyperliquid": "0xOwNeR"}
	record, err := store.GetDurableSession(ctx, "legacy-session")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record.AccountBindings, map[string]string{
		"pacifica": "sol-owner", "hyperliquid": "0xowner",
	}) {
		t.Fatalf("legacy session bindings = %+v", record.AccountBindings)
	}
	position, err := store.GetPositionForBindings(ctx, "legacy-position", bindings)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(position.AccountBindings, record.AccountBindings) {
		t.Fatalf("legacy position bindings = %+v", position.AccountBindings)
	}
}

func TestAtomicPositionPersistenceUsesGenericBindings(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "generic-position.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	result := &executor.ExecutionResult{
		PlanID: "plan-generic", OpportunityID: "opportunity", Asset: "SOL",
		State: executor.ExecStateOpen, StartedAt: time.Now(),
	}
	bindings := map[string]string{"alpha": "owner-a", "beta": "owner-b"}
	if err := store.PersistFullResultAtomicForBindings(
		context.Background(), result, "alpha", "beta",
		map[string]string{"gamma": "owner-a", "delta": "owner-b"}, 10, 2,
	); err == nil {
		t.Fatal("position accepted bindings for a different venue pair")
	}
	if err := store.PersistFullResultAtomicForBindings(
		context.Background(), result, "alpha", "beta", bindings, 10, 2,
	); err != nil {
		t.Fatal(err)
	}

	position, err := store.GetPositionForBindings(context.Background(), "plan-generic", bindings)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(position.AccountBindings, bindings) {
		t.Fatalf("position bindings = %+v, want %+v", position.AccountBindings, bindings)
	}
	if _, err := store.GetPositionForBindings(context.Background(), "plan-generic", map[string]string{
		"alpha": "owner-a", "beta": "other-owner",
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("mismatched binding lookup error = %v, want sql.ErrNoRows", err)
	}
	if _, err := database.Exec(`
		UPDATE live_positions SET account_bindings_json = 'not-json'
		WHERE id = 'plan-generic'`); err != nil {
		t.Fatal(err)
	}
	positions, err := store.ListOpenPositions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].State != string(executor.ExecStateDegraded) ||
		positions[0].RecoveryError == "" || !reflect.DeepEqual(positions[0].AccountBindings, bindings) {
		t.Fatalf("corrupt position = %+v, want degraded position with recovered ownership", positions)
	}
	if _, err := database.Exec(`
		UPDATE live_positions
		SET state = 'closed', account_bindings_json = '{"alpha":"other","beta":"owners"}'
		WHERE id = 'plan-generic'`); err != nil {
		t.Fatal(err)
	}
	positions, err = store.ListPositions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].State != string(executor.ExecStateClosed) ||
		positions[0].RecoveryError == "" || !reflect.DeepEqual(positions[0].AccountBindings, bindings) {
		t.Fatalf("corrupt terminal position = %+v, want closed state with key ownership", positions)
	}
	if _, err := database.Exec(`
		UPDATE live_positions
		SET state = 'open', venue_a = 'gamma', venue_b = 'delta',
			account_bindings_json = account_bindings_key
		WHERE id = 'plan-generic'`); err != nil {
		t.Fatal(err)
	}
	positions, err = store.ListOpenPositions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].State != string(executor.ExecStateDegraded) ||
		positions[0].RecoveryError == "" {
		t.Fatalf("venue mismatch position = %+v, want degraded recovery error", positions)
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
			Accepted: true, Filled: true, RequestedAmt: 10, FilledAmount: 9.8, FillRatio: 0.98,
		},
	}
	if err := store.PersistFullResultAtomic(
		context.Background(), result, "pacifica", "hyperliquid",
		"sol-wallet", "0xwallet", 10, 2,
	); err != nil {
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
	positions, err := store.ListPositionsForAccounts(context.Background(), "sol-wallet", "0xWallet")
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].ID != "plan-1" {
		t.Fatalf("scoped positions = %+v, want plan-1", positions)
	}
	if positions[0].OpenedAt == "" || positions[0].CompletedAt != "" {
		t.Fatalf("position timestamps = opened %q completed %q, want open without completion",
			positions[0].OpenedAt, positions[0].CompletedAt)
	}
	if math.Abs(positions[0].HedgeMismatch-0.02) > 1e-9 {
		t.Fatalf("hedge mismatch = %v, want 0.02", positions[0].HedgeMismatch)
	}
	other, err := store.ListPositionsForAccounts(context.Background(), "other-wallet", "0xother")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other account positions = %+v, want none", other)
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
	if err := store.UpsertRecoveryBlockedPosition(
		ctx, "blocked-session", "SOL", "sol-wallet", "0xwallet", "invalid payload",
	); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM live_positions WHERE id = 'recovery-blocked-session'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(executor.ExecStateDegraded) {
		t.Fatalf("operator-visible recovery position state = %q, want degraded", state)
	}
	if err := store.UpsertRecoveryBlockedPosition(
		ctx, "blocked-session", "SOL", "sol-wallet", "0xwallet", "invalid payload",
	); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM live_events
		WHERE position_id = 'recovery-blocked-session' AND event = 'session_recovery_blocked'
	`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("recovery blocked events = %d, want 1", events)
	}
	if err := store.UpsertUnownedRecoveryBlockedPosition(
		ctx, "unowned-session", "BTC", "invalid ownership",
	); err != nil {
		t.Fatal(err)
	}
	var bindingsKey string
	if err := database.QueryRow(`
		SELECT account_bindings_key FROM live_positions WHERE id = 'recovery-unowned-session'
	`).Scan(&bindingsKey); err != nil {
		t.Fatal(err)
	}
	if bindingsKey != "" {
		t.Fatalf("unowned recovery binding key = %q, want empty", bindingsKey)
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

func TestSupersedeSafeDurableSessionsAllowsImmediateRetry(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "supersede-safe.db"))
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

	ids, err := store.SupersedeSafeDurableSessions(ctx, "sol-wallet", "0xwallet", "SOL")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "session-1" {
		t.Fatalf("superseded sessions = %v, want session-1", ids)
	}
	record.ID = "session-2"
	if err := store.UpsertDurableSession(ctx, record); err != nil {
		t.Fatalf("immediate retry after safe session: %v", err)
	}
}

func TestSupersedeSafeDurableSessionsPreservesPossibleExposure(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "preserve-exposure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	record := executor.DurableSessionRecord{
		ID: "session-1", State: "leg1_submitting", Payload: []byte(`{}`),
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet", Asset: "SOL",
		HasExposure: true, ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := store.UpsertDurableSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	ids, err := store.SupersedeSafeDurableSessions(ctx, "sol-wallet", "0xwallet", "SOL")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("superseded exposed sessions = %v, want none", ids)
	}
	active, err := store.ActiveDurableSessionForAccountAsset(ctx, "sol-wallet", "0xwallet", "SOL")
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != "session-1" || !active.HasExposure {
		t.Fatalf("active session = %+v, want exposed session-1", active)
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
