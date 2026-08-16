package executor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type fakeFundingHistory struct {
	payments []venue.FundingPayment
	calls    int
	err      error
	since    time.Time
	until    time.Time
}

func (f *fakeFundingHistory) FundingPayments(_ context.Context, _, _ string, since, until time.Time) ([]venue.FundingPayment, error) {
	f.calls++
	f.since = since
	f.until = until
	return f.payments, f.err
}

func TestFinalFundingUsesActualHoldingInterval(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "funding-interval.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	startedAt := now.Add(-3 * time.Hour)
	openedAt := now.Add(-2 * time.Hour)
	completedAt := now.Add(-time.Minute)
	if _, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_pacifica, account_hyperliquid, notional, leverage,
			started_at, opened_at, completed_at, updated_at
		) VALUES ('position-1', 'plan-1', 'opp-1', 'SOL', 'pacifica', 'hyperliquid', 'closed',
			'sol-wallet', '0xwallet', 100, 2, ?, ?, ?, ?)`,
		startedAt.Format(time.RFC3339), openedAt.Format(time.RFC3339),
		completedAt.Format(time.RFC3339), completedAt.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pacifica := &fakeFundingHistory{}
	hyperliquid := &fakeFundingHistory{}
	monitor := NewFundingMonitor(logger, NewStore(database, logger), map[string]venue.FundingHistory{
		"pacifica": pacifica, "hyperliquid": hyperliquid,
	})

	monitor.finalizeClosed(context.Background())

	for name, source := range map[string]*fakeFundingHistory{"pacifica": pacifica, "hyperliquid": hyperliquid} {
		if source.calls != 1 || !source.since.Equal(openedAt) || !source.until.Equal(completedAt) {
			t.Fatalf("%s interval = [%s, %s] calls=%d, want [%s, %s] once",
				name, source.since, source.until, source.calls, openedAt, completedAt)
		}
	}
}

func TestPartialFundingSyncIsNotPublished(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "partial-funding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	startedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if _, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_pacifica, account_hyperliquid, notional, leverage, started_at, opened_at, updated_at
		) VALUES ('position-1', 'plan-1', 'opp-1', 'SOL', 'pacifica', 'hyperliquid', 'open',
			'sol-wallet', '0xwallet', 100, 2, ?, ?, ?)`,
		startedAt.Format(time.RFC3339), startedAt.Format(time.RFC3339), startedAt.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStore(database, logger)
	pacifica := &fakeFundingHistory{payments: []venue.FundingPayment{{
		ExternalID: "pac-1", Venue: "pacifica", Account: "sol-wallet", Asset: "SOL", AmountUSD: 0.02, PaidAt: startedAt.Add(time.Hour),
	}}}
	hyperliquid := &fakeFundingHistory{err: errors.New("unavailable")}
	monitor := NewFundingMonitor(logger, store, map[string]venue.FundingHistory{
		"pacifica": pacifica, "hyperliquid": hyperliquid,
	})
	position, err := store.GetPosition(context.Background(), "position-1")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if total, ok := monitor.realized(context.Background(), position, startedAt, time.Now(), false); ok || total != 0 {
			t.Fatalf("partial funding published: total = %v, ok = %v", total, ok)
		}
	}
	if pacifica.calls != 1 || hyperliquid.calls != 1 {
		t.Fatalf("partial sync was not throttled: Pacifica %d Hyperliquid %d", pacifica.calls, hyperliquid.calls)
	}
}

func TestRealizedFundingSumsVenueLedgersWithoutDuplicates(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "funding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	openedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	if _, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_pacifica, account_hyperliquid, notional, leverage, started_at, opened_at, updated_at
		) VALUES ('position-1', 'plan-1', 'opp-1', 'SOL', 'pacifica', 'hyperliquid', 'open',
			'sol-wallet', '0xwallet', 100, 2, ?, ?, ?)`,
		openedAt.Format(time.RFC3339), openedAt.Format(time.RFC3339), openedAt.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStore(database, logger)
	pacifica := &fakeFundingHistory{payments: []venue.FundingPayment{{
		ExternalID: "pac-1", Venue: "pacifica", Account: "sol-wallet", Asset: "SOL", AmountUSD: 0.02, PaidAt: openedAt.Add(time.Hour),
	}}}
	hyperliquid := &fakeFundingHistory{payments: []venue.FundingPayment{{
		ExternalID: "hl-1", Venue: "hyperliquid", Account: "0xwallet", Asset: "SOL", AmountUSD: -0.01, PaidAt: openedAt.Add(time.Hour),
	}}}
	monitor := NewFundingMonitor(logger, store, map[string]venue.FundingHistory{
		"pacifica": pacifica, "hyperliquid": hyperliquid,
	})
	position, err := store.GetPosition(context.Background(), "position-1")
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		total, ok := monitor.realized(context.Background(), position, openedAt, time.Now(), false)
		if !ok || total != 0.01 {
			t.Fatalf("realized funding = %v, ok = %v", total, ok)
		}
	}
	if pacifica.calls != 1 || hyperliquid.calls != 1 {
		t.Fatalf("funding calls = Pacifica %d Hyperliquid %d", pacifica.calls, hyperliquid.calls)
	}
	total, ok := monitor.realized(context.Background(), position, openedAt, time.Now(), true)
	if !ok || total != 0.01 {
		t.Fatalf("deduplicated funding = %v, ok = %v", total, ok)
	}
	if pacifica.calls != 2 || hyperliquid.calls != 2 {
		t.Fatalf("forced funding calls = Pacifica %d Hyperliquid %d", pacifica.calls, hyperliquid.calls)
	}
	completedAt := time.Now().UTC().Add(-fundingFinalizationDelay - time.Second).Truncate(time.Second)
	if _, err := database.Exec(`UPDATE live_positions SET state = 'closed', completed_at = ? WHERE id = 'position-1'`, completedAt.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	monitor.finalizeClosed(context.Background())
	var fundingPnL float64
	var finalized int
	var source string
	if err := database.QueryRow(`
		SELECT position.funding_pnl, position.funding_pnl_source, sync.finalized
		FROM live_positions position JOIN live_funding_sync sync ON sync.position_id = position.id
		WHERE position.id = 'position-1'`).Scan(&fundingPnL, &source, &finalized); err != nil {
		t.Fatal(err)
	}
	if fundingPnL != 0.01 || source != "realized" || finalized != 1 {
		t.Fatalf("final funding = %v, source = %q, finalized = %d", fundingPnL, source, finalized)
	}
}

func TestPriceAndFundingUpdatesRecomputeTotalAtomically(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "pnl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			notional, leverage, started_at, opened_at, updated_at
		) VALUES ('position-1', 'plan-1', 'opp-1', 'SOL', 'pacifica', 'hyperliquid', 'open',
			100, 2, ?, ?, ?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	store := NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	store.UpdateMonitoring(context.Background(), "position-1", MonitorUpdate{PricePnL: -1.40})
	if err := store.UpdateRealizedFunding(context.Background(), "position-1", 1.35); err != nil {
		t.Fatal(err)
	}
	store.UpdateMonitoring(context.Background(), "position-1", MonitorUpdate{PricePnL: -1.40})

	var total float64
	if err := database.QueryRow(`SELECT total_pnl FROM live_positions WHERE id = 'position-1'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if math.Abs(total-(-0.05)) > 1e-9 {
		t.Fatalf("total PnL = %v, want -0.05", total)
	}
}
