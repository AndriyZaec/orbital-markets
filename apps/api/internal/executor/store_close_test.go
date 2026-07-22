package executor_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestCloseProgressRequiresConfirmedFillForEveryOpenLeg(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "close.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	seedLivePosition(t, database, "position-1")
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := store.UpsertCloseOutcome(ctx, executor.CloseOutcome{
		PositionID: "position-1", Leg: 1, Venue: "pacifica", ClientOrderID: "close-1",
		RequestedAmount: 10, Accepted: true,
	}); err != nil {
		t.Fatal(err)
	}
	progress, err := store.GetCloseProgress(ctx, "position-1")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Pending != 1 || progress.Confirmed != 0 || progress.Failed != 0 {
		t.Fatalf("pending progress = %+v, want pending=1", progress)
	}

	if err := store.UpsertCloseOutcome(ctx, executor.CloseOutcome{
		PositionID: "position-1", Leg: 1, Venue: "pacifica", ClientOrderID: "close-1",
		RequestedAmount: 10, FilledAmount: 10, FillRatio: 1, Accepted: true, Confirmed: true, Resolved: true,
	}); err != nil {
		t.Fatal(err)
	}
	progress, err = store.GetCloseProgress(ctx, "position-1")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Required != 2 || progress.Confirmed != 1 || progress.Failed != 0 {
		t.Fatalf("progress = %+v, want required=2 confirmed=1 failed=0", progress)
	}

	if err := store.UpsertCloseOutcome(ctx, executor.CloseOutcome{
		PositionID: "position-1", Leg: 2, Venue: "hyperliquid", ClientOrderID: "close-2",
		RequestedAmount: 10, FilledAmount: 10, FillRatio: 1, Accepted: true, Confirmed: true, Resolved: true,
	}); err != nil {
		t.Fatal(err)
	}
	progress, err = store.GetCloseProgress(ctx, "position-1")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Confirmed != progress.Required {
		t.Fatalf("progress = %+v, want all legs confirmed", progress)
	}
	changed, err := store.MarkClosed(ctx, "position-1")
	if err != nil || !changed {
		t.Fatalf("first MarkClosed() = %v, %v; want true, nil", changed, err)
	}
	changed, err = store.MarkClosed(ctx, "position-1")
	if err != nil || changed {
		t.Fatalf("second MarkClosed() = %v, %v; want false, nil", changed, err)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM live_positions WHERE id = 'position-1'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(executor.ExecStateClosed) {
		t.Fatalf("state = %q, want closed", state)
	}
}

func TestCloseProgressTreatsResolvedPartialFillAsFailure(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "close.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	seedLivePosition(t, database, "position-2")
	store := executor.NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := store.UpsertCloseOutcome(ctx, executor.CloseOutcome{
		PositionID: "position-2", Leg: 1, Venue: "pacifica", ClientOrderID: "close-partial",
		RequestedAmount: 10, FilledAmount: 4, FillRatio: 0.4, Accepted: true, Confirmed: false, Resolved: true,
	}); err != nil {
		t.Fatal(err)
	}
	progress, err := store.GetCloseProgress(ctx, "position-2")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Failed != 1 || progress.Confirmed != 0 {
		t.Fatalf("progress = %+v, want one failed leg", progress)
	}
	if err := store.UpsertCloseOutcome(ctx, executor.CloseOutcome{
		PositionID: "position-2", Leg: 1, Venue: "pacifica", ClientOrderID: "close-retry",
		RequestedAmount: 10, FilledAmount: 10, FillRatio: 1, Accepted: true, Confirmed: true, Resolved: true,
	}); err != nil {
		t.Fatal(err)
	}
	progress, err = store.GetCloseProgress(ctx, "position-2")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Failed != 0 || progress.Confirmed != 1 {
		t.Fatalf("retry progress = %+v, want latest leg attempt confirmed", progress)
	}
	if err := store.MarkCloseDegraded(ctx, "position-2"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM live_positions WHERE id = 'position-2'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(executor.ExecStateDegraded) {
		t.Fatalf("state = %q, want degraded", state)
	}
}

func seedLivePosition(t *testing.T, database *sql.DB, positionID string) {
	t.Helper()
	const now = "2026-07-22T12:00:00Z"
	_, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			notional, leverage, entry_spread, hedge_mismatch, started_at, opened_at, updated_at
		) VALUES (?, 'plan-1', 'opportunity-1', 'SOL', 'pacifica', 'hyperliquid', 'open',
			10, 2, 0, 0, ?, ?, ?)`, positionID, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	for leg, venue := range []string{"pacifica", "hyperliquid"} {
		_, err = database.Exec(`
			INSERT INTO live_fills (
				position_id, leg, venue, symbol, side, order_id, client_order_id,
				requested_amount, filled_amount, avg_fill_price, fill_ratio, fee,
				accepted, filled, error, filled_at
			) VALUES (?, ?, ?, 'SOL', 'buy', ?, ?, 10, 10, 100, 1, 0, 1, 1, '', ?)`,
			positionID, leg+1, venue, "open-order", "open-client", now)
		if err != nil {
			t.Fatal(err)
		}
	}
}
