package executor

import (
	"context"
	"errors"
	"fmt"
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
	account  string
	since    time.Time
	until    time.Time
}

func (f *fakeFundingHistory) FundingPayments(_ context.Context, account, _ string, since, until time.Time) ([]venue.FundingPayment, error) {
	f.calls++
	f.account = account
	f.since = since
	f.until = until
	if f.err != nil {
		return nil, f.err
	}
	payments := make([]venue.FundingPayment, 0, len(f.payments))
	for _, payment := range f.payments {
		if !payment.PaidAt.Before(since) && !payment.PaidAt.After(until) {
			payments = append(payments, payment)
		}
	}
	return payments, nil
}

func TestFundingUsesPersistedGenericVenueBindings(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "generic-funding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStore(database, logger)
	startedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	result := &ExecutionResult{
		PlanID: "generic-position", OpportunityID: "opportunity", Asset: "SOL",
		State: ExecStateOpen, StartedAt: startedAt,
	}
	if err := store.PersistFullResultAtomicForBindings(
		context.Background(), result, "alpha", "beta",
		map[string]string{"alpha": "owner-a", "beta": "owner-b"}, 10, 2,
	); err != nil {
		t.Fatal(err)
	}
	position, err := store.GetPosition(context.Background(), result.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	alpha := &fakeFundingHistory{}
	beta := &fakeFundingHistory{}
	monitor := NewFundingMonitor(logger, store, map[string]venue.FundingHistory{
		"alpha": alpha, "beta": beta,
	})

	if _, ok := monitor.realized(context.Background(), position, startedAt, time.Now().UTC(), true); !ok {
		t.Fatal("generic funding sync was not published")
	}
	if alpha.calls != 1 || alpha.account != "owner-a" || beta.calls != 1 || beta.account != "owner-b" {
		t.Fatalf("funding calls alpha=%d/%q beta=%d/%q", alpha.calls, alpha.account, beta.calls, beta.account)
	}
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

func TestLongHeldFundingResumesCoverageAfterRestartAndCompactsRawRows(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "long-funding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	openedAt := time.Now().UTC().Add(-45 * 24 * time.Hour).Truncate(time.Second)
	target := openedAt.Add(45 * 24 * time.Hour)
	if _, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_bindings_json, account_bindings_key, notional, leverage,
			started_at, opened_at, updated_at
		) VALUES ('position-long', 'plan-long', 'opp-long', 'SOL', 'aster', 'hyperliquid', 'open',
			'{"aster":"0xaster","hyperliquid":"0xhyper"}',
			'{"aster":"0xaster","hyperliquid":"0xhyper"}', 100, 2, ?, ?, ?)`,
		openedAt.Format(time.RFC3339), openedAt.Format(time.RFC3339), openedAt.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	payments := func(venueName, account, prefix string, amount float64) []venue.FundingPayment {
		days := []float64{1, 6.5, 20, 30, 44}
		result := make([]venue.FundingPayment, 0, len(days))
		for index, day := range days {
			result = append(result, venue.FundingPayment{
				ExternalID: fmt.Sprintf("%s-%d", prefix, index), Venue: venueName, Account: account,
				Asset: "SOL", AmountUSD: amount, PaidAt: openedAt.Add(time.Duration(day * float64(24*time.Hour))),
			})
		}
		return result
	}
	aster := &fakeFundingHistory{payments: payments("aster", "0xaster", "aster", 0.2)}
	hyperliquid := &fakeFundingHistory{payments: payments("hyperliquid", "0xhyper", "hyper", -0.1)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStore(database, logger)
	position, err := store.GetPosition(context.Background(), "position-long")
	if err != nil {
		t.Fatal(err)
	}
	monitor := NewFundingMonitor(logger, store, map[string]venue.FundingHistory{
		"aster": aster, "hyperliquid": hyperliquid,
	})
	if total, complete := monitor.realized(context.Background(), position, openedAt, target, true); complete || total != 0 {
		t.Fatalf("first bounded pass total = %v, complete = %v", total, complete)
	}

	restartedStore := NewStore(database, logger)
	restarted := NewFundingMonitor(logger, restartedStore, map[string]venue.FundingHistory{
		"aster": aster, "hyperliquid": hyperliquid,
	})
	total, complete := restarted.realized(context.Background(), position, openedAt, target, true)
	if !complete || math.Abs(total-0.5) > 1e-9 {
		rows, queryErr := database.Query(`SELECT venue, covered_through_ms, amount_usd FROM live_funding_coverage WHERE position_id = 'position-long'`)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		defer rows.Close()
		var diagnostic []string
		for rows.Next() {
			var venueName string
			var through int64
			var amount float64
			if err := rows.Scan(&venueName, &through, &amount); err != nil {
				t.Fatal(err)
			}
			diagnostic = append(diagnostic, fmt.Sprintf("%s:%s:%v", venueName, time.UnixMilli(through), amount))
		}
		t.Fatalf("resumed total = %v, complete = %v, coverage = %v, target = %s", total, complete, diagnostic, target)
	}
	var coverageRows, rawRows int
	var aggregate float64
	if err := database.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(amount_usd), 0)
		FROM live_funding_coverage WHERE position_id = 'position-long'`).Scan(&coverageRows, &aggregate); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM live_funding_payments WHERE position_id = 'position-long'`).Scan(&rawRows); err != nil {
		t.Fatal(err)
	}
	if coverageRows != 2 || math.Abs(aggregate-0.5) > 1e-9 || rawRows != 2 {
		t.Fatalf("coverage rows = %d, aggregate = %v, raw rows = %d", coverageRows, aggregate, rawRows)
	}
	if total, complete, err := restartedStore.ReconcileFunding(context.Background(), position, target, true); err != nil || !complete || math.Abs(total-0.5) > 1e-9 {
		t.Fatalf("finalized total = %v, complete = %v, err = %v", total, complete, err)
	}
	latePayment := venue.FundingPayment{
		ExternalID: "aster-late", Venue: "aster", Account: "0xaster", Asset: "SOL",
		AmountUSD: 99, PaidAt: openedAt.Add(44 * 24 * time.Hour),
	}
	if err := restartedStore.ApplyObservedFunding(
		context.Background(), position.ID, "aster", "0xaster", position.Asset, openedAt, []venue.FundingPayment{latePayment},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT COALESCE(SUM(amount_usd), 0),
			(SELECT COUNT(*) FROM live_funding_payments WHERE position_id = 'position-long')
		FROM live_funding_coverage WHERE position_id = 'position-long'`).Scan(&aggregate, &rawRows); err != nil {
		t.Fatal(err)
	}
	if math.Abs(aggregate-0.5) > 1e-9 || rawRows != 0 {
		t.Fatalf("post-finalization aggregate = %v, raw rows = %d", aggregate, rawRows)
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

	var funding, total float64
	var source string
	if err := database.QueryRow(`
		SELECT funding_pnl, funding_pnl_source, total_pnl
		FROM live_positions WHERE id = 'position-1'
	`).Scan(&funding, &source, &total); err != nil {
		t.Fatal(err)
	}
	if funding != 1.35 || source != "realized" || math.Abs(total-(-0.05)) > 1e-9 {
		t.Fatalf("funding PnL = %v (%s), total PnL = %v; want 1.35 realized and -0.05 total", funding, source, total)
	}
}

func TestAsterIncomeSyncClaimIsAccountScopedAndHourly(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "income-claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	store := NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now().UTC()
	claimed, err := store.ClaimAsterIncomeSync(context.Background(), "0xAbCd", now)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, err = %v", claimed, err)
	}
	claimed, err = store.ClaimAsterIncomeSync(context.Background(), "0xabcd", now.Add(59*time.Minute))
	if err != nil || claimed {
		t.Fatalf("early claim = %v, err = %v", claimed, err)
	}
	claimed, err = store.ClaimAsterIncomeSync(context.Background(), "0xabcd", now.Add(time.Hour))
	if err != nil || !claimed {
		t.Fatalf("hourly claim = %v, err = %v", claimed, err)
	}
}

func TestRealizedFundingRequiresBothVenueSnapshots(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "venue-sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_bindings_json, account_bindings_key, notional, leverage, started_at, opened_at, updated_at
		) VALUES ('position-1', 'plan-1', 'opp-1', 'SOL', 'aster', 'pacifica', 'open',
			'{"aster":"0xabc","pacifica":"wallet"}', 'aster=0xabc|pacifica=wallet', 100, 2, ?, ?, ?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	store := NewStore(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	position, err := store.GetPosition(context.Background(), "position-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFundingVenueSync(context.Background(), position.ID, "aster", false); err != nil {
		t.Fatal(err)
	}
	if complete, err := store.FundingVenueSyncComplete(context.Background(), position, false); err != nil || complete {
		t.Fatalf("one venue complete = %v, err = %v", complete, err)
	}
	if err := store.RecordFundingVenueSync(context.Background(), position.ID, "pacifica", false); err != nil {
		t.Fatal(err)
	}
	if complete, err := store.FundingVenueSyncComplete(context.Background(), position, false); err != nil || !complete {
		t.Fatalf("both venues complete = %v, err = %v", complete, err)
	}
}
