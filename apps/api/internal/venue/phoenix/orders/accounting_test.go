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
