package executor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"
)

type delayedFundingHistory struct{ fakeFundingHistory }

func (*delayedFundingHistory) FundingFinalizationDelay() time.Duration {
	return 24*time.Hour + 5*time.Minute
}

func TestDelayedSettlementVenuePreventsPrematureClosedFundingFinalization(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "settlement-finalization.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStore(db, db, logger)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	opened := now.Add(-49 * time.Hour)
	result := &ExecutionResult{PlanID: "daily-settlement", Asset: "SOL", State: ExecStateClosed, StartedAt: opened}
	if err := store.PersistFullResultAtomicForBindings(ctx, result, "phoenix", "hyperliquid", map[string]string{"phoenix": "phi-owner", "hyperliquid": "hl-owner"}, 100, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE live_positions SET opened_at=?,completed_at=? WHERE id=?`, opened.Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339), result.PlanID); err != nil {
		t.Fatal(err)
	}
	phoenix, hl := &delayedFundingHistory{}, &fakeFundingHistory{}
	monitor := NewFundingMonitor(logger, store, map[string]venue.FundingHistory{"phoenix": phoenix, "hyperliquid": hl})
	monitor.finalizeClosed(ctx)
	if phoenix.calls != 0 || hl.calls != 0 {
		t.Fatal("daily venue finalized on shared 30s delay")
	}
	completed := now.Add(-26 * time.Hour)
	if _, err := db.Exec(`UPDATE live_positions SET completed_at=? WHERE id=?`, completed.Format(time.RFC3339), result.PlanID); err != nil {
		t.Fatal(err)
	}
	monitor.finalizeClosed(ctx)
	if phoenix.calls != 1 || hl.calls != 1 || !phoenix.until.Equal(completed) {
		t.Fatal("settlement finalization did not use holding interval", phoenix.calls, hl.calls, phoenix.until)
	}
}

func TestObservedPhoenixFeeBackfillDoesNotOverwriteMergedRetryWithLastFee(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "observed-fees.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	const owner = "11111111111111111111111111111111"
	result := &ExecutionResult{PlanID: "fee-position", Asset: "SOL", State: ExecStateOpen, StartedAt: time.Now(), Leg2: LegResult{Venue: "phoenix", Symbol: "SOL", Side: "short", Submitted: true, Accepted: true, Filled: true, FilledAmount: 1, RequestedAmt: 1, AvgFillPrice: 100, Fee: .15, ClientOrderID: "43", OrderID: "retry-signature"}}
	if err := store.PersistFullResultAtomicForBindings(ctx, result, "hyperliquid", "phoenix", map[string]string{"hyperliquid": "hl-owner", "phoenix": owner}, 100, 2); err != nil {
		t.Fatal(err)
	}
	first := domain.SigningRequest{ID: "open-first", ClientOrderID: "42", Venue: "phoenix", Account: owner, Symbol: "SOL", Action: "open"}
	retry := domain.SigningRequest{ID: "open-retry", ClientOrderID: "43", Venue: "phoenix", Account: owner, Symbol: "SOL", Action: "open"}
	payload, _ := json.Marshal(map[string]any{"plan": map[string]any{"id": result.PlanID}, "leg_2_open_request": first, "leg_2_retry_request": retry})
	if err := store.UpsertDurableSession(ctx, DurableSessionRecord{ID: "fee-session", Asset: "SOL", State: "open", Payload: payload, AccountBindings: map[string]string{"hyperliquid": "hl-owner", "phoenix": owner}}); err != nil {
		t.Fatal(err)
	}
	journal, _ := store.PhoenixOrderJournal()
	firstFee := "2000"
	for _, entry := range []struct {
		request                  domain.SigningRequest
		signature, quantity, fee string
		accounting               *orders.ObservedAccounting
	}{
		{first, "first-signature", "0.6", "0.10", &orders.ObservedAccounting{Slot: 130, Events: helper.AccountingResult{FlightFeeQuoteLots: &firstFee}}},
		{retry, "retry-signature", "0.4", "0.05", nil},
	} {
		a := orders.Attempt{Request: entry.request, Signature: entry.signature, CreatedAt: time.Now()}
		if ok, err := journal.Claim(ctx, a); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if err := journal.Resolve(ctx, a, "filled", &orders.Fill{BaseQuantity: entry.quantity, ExchangeFee: entry.fee, Slot: 130, Accounting: entry.accounting}, ""); err != nil {
			t.Fatal(err)
		}
	}
	position, err := store.GetPosition(ctx, result.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncPhoenixObservedFees(ctx, position); err != nil {
		t.Fatal(err)
	}
	fills, _ := store.GetFills(ctx, position.ID)
	if len(fills) != 1 || fills[0].Fee != .15 {
		t.Fatal("partial accounting replaced aggregate fee", fills)
	}
	a, err := journal.Get(ctx, owner, retry.ID)
	if err != nil {
		t.Fatal(err)
	}
	retryFee := "1000"
	if err := journal.AttachAccounting(ctx, a, orders.ObservedAccounting{Slot: 130, Events: helper.AccountingResult{FlightFeeQuoteLots: &retryFee}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.SyncPhoenixObservedFees(ctx, position); err != nil {
			t.Fatal(err)
		}
	}
	fills, _ = store.GetFills(ctx, position.ID)
	if math.Abs(fills[0].Fee-.153) > 1e-12 {
		t.Fatal("not all observed exchange/Flight fees were combined", fills[0].Fee)
	}
}

func TestPhoenixFundingCannotCreditSharedMarketPaymentToSmallerStrategy(t *testing.T) {
	db, err := appdb.Open(filepath.Join(t.TempDir(), "shared-funding.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	result := &ExecutionResult{PlanID: "shared-position", Asset: "SOL", State: ExecStateOpen, StartedAt: time.Now(), Leg1: LegResult{Venue: "phoenix", Symbol: "SOL-PERP", Side: "long", Submitted: true, Filled: true, FilledAmount: 1, AvgFillPrice: 100}}
	if err := store.PersistFullResultAtomicForBindings(ctx, result, "phoenix", "hyperliquid", map[string]string{"phoenix": "phi-owner", "hyperliquid": "hl-owner"}, 100, 2); err != nil {
		t.Fatal(err)
	}
	position, err := store.GetPosition(ctx, result.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := time.Parse(time.RFC3339, position.OpenedAt)
	if err != nil {
		t.Fatal(err)
	}
	window := FundingWindow{Since: opened, Until: opened.Add(time.Hour)}
	payment := venue.FundingPayment{ExternalID: "settlement-1", Venue: "phoenix", Account: "phi-owner", Asset: "SOL", MarketKey: "SOL-PERP", AmountUSD: .02, PaidAt: opened.Add(30 * time.Minute), PositionSize: "2", PositionSide: "long"}
	if err := store.ApplyFundingWindow(ctx, position.ID, "phoenix", "phi-owner", "SOL", opened, window, []venue.FundingPayment{payment}); err == nil {
		t.Fatal("account-wide funding was credited to smaller strategy")
	}
	if total, err := store.SumFundingPayments(ctx, position.ID); err != nil || total != 0 {
		t.Fatal("failed allocation advanced cash accounting", total, err)
	}
	payment.PositionSize = "1"
	if err := store.ApplyFundingWindow(ctx, position.ID, "phoenix", "phi-owner", "SOL", opened, window, []venue.FundingPayment{payment}); err != nil {
		t.Fatal(err)
	}
	if total, err := store.SumFundingPayments(ctx, position.ID); err != nil || math.Abs(total-.02) > 1e-12 {
		t.Fatal(total, err)
	}
}
