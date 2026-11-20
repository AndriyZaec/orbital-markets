package orders

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
)

type accountingBackend struct {
	*testBackend
	calls    int
	observed ObservedAccounting
	fail     bool
}

func (b *accountingBackend) OrderAccounting(context.Context, *domain.SigningRequest, string) (ObservedAccounting, error) {
	b.calls++
	if b.fail {
		return ObservedAccounting{}, errors.New("event RPC unavailable")
	}
	return b.observed, nil
}

func TestObservedAccountingIsSeparateFromFillDiscoveryAndDurableAfterBackfill(t *testing.T) {
	s, base, q, _ := testService(t, nil)
	p, err := Decode(q)
	if err != nil {
		t.Fatal(err)
	}
	a := Attempt{Request: *q, Signature: "accounted-signature", CreatedAt: time.Now()}
	if ok, err := s.store.Claim(context.Background(), a); err != nil || !ok {
		t.Fatal(ok, err)
	}
	fill := &Fill{BaseLots: "15", BaseQuantity: "0.15", QuoteQuantity: "15", ExchangeFee: "0.005", Slot: 130, Status: "partial_fill"}
	if err := s.store.Resolve(context.Background(), a, "filled", fill, ""); err != nil {
		t.Fatal(err)
	}
	zero := "0"
	b := &accountingBackend{testBackend: base, observed: ObservedAccounting{Slot: 130, NetworkFeeLamports: "10000", Events: helper.AccountingResult{OwnerAuthority: q.Account, TraderAccount: p.Metadata.Built.TraderAccount, FlightFeeQuoteLots: &zero, DecodedEventCount: 1}}}
	s.backend = b
	a, err = s.Reconcile(context.Background(), q.Account, q.ID, nil)
	if err != nil || b.calls != 0 || a.Fill.Accounting != nil {
		t.Fatal("accounting delayed/altered confirmed exposure", a, err)
	}
	b.fail = true
	if _, err := s.Account(context.Background(), a); err == nil {
		t.Fatal("missing fee evidence became complete")
	}
	b.fail = false
	a, err = s.Account(context.Background(), a)
	if err != nil || a.Fill.Accounting.NetworkFeeLamports != "10000" {
		t.Fatal(a, err)
	}
	fee, complete := a.Fill.TotalTradingFee()
	if !complete || fee.RatString() != "1/200" {
		t.Fatal(fee, complete)
	}
	count := b.calls
	if _, err := s.Account(context.Background(), a); err != nil || b.calls != count {
		t.Fatal("accounting was fetched twice", err)
	}
	ready, err := s.store.Unaccounted(context.Background(), q.Account)
	if err != nil || len(ready) != 0 {
		t.Fatal(ready, err)
	}
	loaded, err := NewService(s.store, b, nil).Reconcile(context.Background(), q.Account, q.ID, nil)
	if err != nil || loaded.Fill.Accounting == nil || b.calls != count {
		t.Fatal("accounting did not survive recreation", err)
	}
}

func TestTradingFeeUsesObservedAmountAndNeverInferredTwoBps(t *testing.T) {
	amount := "12345"
	f := Fill{ExchangeFee: "0.005", Accounting: &ObservedAccounting{Events: helper.AccountingResult{FlightFeeQuoteLots: &amount}}}
	fee, complete := f.TotalTradingFee()
	if !complete || fee.RatString() != "3469/200000" {
		t.Fatal(fee, complete)
	}
	f.Accounting.Events.FlightFeeQuoteLots = nil
	fee, complete = f.TotalTradingFee()
	if complete || fee.RatString() != "1/200" {
		t.Fatal("missing transfer was guessed as 2 bps or zero", fee, complete)
	}
}

func TestPersistedAccountingWithoutFlightFeeIsNotFetchedAgain(t *testing.T) {
	ctx := context.Background()
	s, base, q, _ := testService(t, nil)
	p, err := Decode(q)
	if err != nil {
		t.Fatal(err)
	}
	a := Attempt{Request: *q, Signature: "missing-flight-fee", CreatedAt: time.Now()}
	if ok, err := s.store.Claim(ctx, a); err != nil || !ok {
		t.Fatal(ok, err)
	}
	fill := &Fill{BaseLots: "15", BaseQuantity: "0.15", QuoteQuantity: "15", ExchangeFee: "0.005", Slot: 130, Status: "partial_fill"}
	if err := s.store.Resolve(ctx, a, "filled", fill, ""); err != nil {
		t.Fatal(err)
	}
	a, err = s.store.Get(ctx, q.Account, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	observed := ObservedAccounting{Slot: 130, NetworkFeeLamports: "10000", Events: helper.AccountingResult{OwnerAuthority: q.Account, TraderAccount: p.Metadata.Built.TraderAccount, DecodedEventCount: 1}}
	if err := s.store.AttachAccounting(ctx, a, observed); err != nil {
		t.Fatal(err)
	}
	a, err = s.store.Get(ctx, q.Account, q.ID)
	if err != nil || a.Fill.Accounting == nil || a.Fill.Accounting.Events.FlightFeeQuoteLots != nil {
		t.Fatal("missing Flight fee accounting did not persist", a, err)
	}
	t.Run("not queued", func(t *testing.T) {
		attempts, err := s.store.Unaccounted(ctx, q.Account)
		if err != nil || len(attempts) != 0 {
			t.Fatalf("persisted accounting was queued again: %d attempts, err=%v", len(attempts), err)
		}
	})
	t.Run("no RPC", func(t *testing.T) {
		b := &accountingBackend{testBackend: base, fail: true}
		recreated := NewService(s.store, b, nil)
		accounted, err := recreated.Account(ctx, a)
		if err != nil || b.calls != 0 {
			t.Fatal("persisted accounting triggered RPC", b.calls, err)
		}
		fee, complete := accounted.Fill.TotalTradingFee()
		if complete || fee.RatString() != "1/200" {
			t.Fatal("missing Flight fee became complete or changed exchange fee", fee, complete)
		}
	})
}

func TestUnaccountedOnlyIncludesFillsCreatedWithin24Hours(t *testing.T) {
	for _, tc := range []struct {
		name       string
		age        time.Duration
		fractional bool
		want       int
	}{
		{"recent fractional", 24*time.Hour - time.Minute, true, 1},
		{"recent whole second", 24*time.Hour - time.Minute, false, 1},
		{"old fractional", 24*time.Hour + time.Minute, true, 0},
		{"old whole second", 24*time.Hour + time.Minute, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, _, q, _ := testService(t, nil)
			createdAt := time.Now().UTC().Add(-tc.age).Truncate(time.Second)
			if tc.fractional {
				createdAt = createdAt.Add(123456789 * time.Nanosecond)
			}
			a := Attempt{Request: *q, Signature: "unaccounted-fill", CreatedAt: createdAt.In(time.FixedZone("UTC+2", 2*60*60))}
			if ok, err := s.store.Claim(ctx, a); err != nil || !ok {
				t.Fatal(ok, err)
			}
			fill := &Fill{BaseLots: "15", BaseQuantity: "0.15", QuoteQuantity: "15", ExchangeFee: "0.005", Slot: 130, Status: "partial_fill"}
			if err := s.store.Resolve(ctx, a, "filled", fill, ""); err != nil {
				t.Fatal(err)
			}
			var storedAt string
			if err := s.store.reader.QueryRowContext(ctx, `SELECT created_at FROM phoenix_order_attempts WHERE request_id=?`, q.ID).Scan(&storedAt); err != nil || storedAt != createdAt.Format(time.RFC3339Nano) {
				t.Fatal("created_at was not stored as RFC3339Nano UTC", storedAt, err)
			}
			attempts, err := s.store.Unaccounted(ctx, q.Account)
			if err != nil || len(attempts) != tc.want {
				t.Fatalf("Unaccounted returned %d attempts, want %d: %v", len(attempts), tc.want, err)
			}
			if len(attempts) != 0 && (attempts[0].Request.ID != q.ID || attempts[0].Fill.Accounting != nil) {
				t.Fatal("incorrect unaccounted fill", attempts)
			}
		})
	}
}
