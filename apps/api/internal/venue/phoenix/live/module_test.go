package live

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type modulePreparer struct {
	last  Intent
	calls int
}

func (*modulePreparer) NormalizeAmount(context.Context, string, float64) (float64, error) {
	return .29, nil
}
func (p *modulePreparer) Prepare(ctx context.Context, q Intent) (*domain.SigningRequest, error) {
	p.last = q
	p.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &domain.SigningRequest{ID: q.ID, ClientOrderID: q.ClientOrderID, Side: q.Side, Account: q.Owner, Signer: q.Agent}, nil
}

func TestLiveModuleUsesPositionSidesAndNativeTrackingIDs(t *testing.T) {
	p := &modulePreparer{}
	m, err := NewLiveModule(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	params := venue.LiveOrderParams{Account: owner, Signer: agent, Symbol: "SOL", Side: domain.SideLong, Amount: .299, Price: 100, LimitPrice: 100.005, ClientOrderID: "orbital-l1open-123"}
	open, err := m.BuildOpen(params)
	if err != nil || open.Side != "buy" || open.ID == "" || open.Account != owner || open.Signer != agent {
		t.Fatalf("open=%+v err=%v", open, err)
	}
	cid, err := strconv.ParseUint(open.ClientOrderID, 10, 64)
	if err != nil || cid == 0 {
		t.Fatal("non-native tracking identity", open.ClientOrderID)
	}
	for _, side := range []domain.Side{domain.SideLong, domain.SideShort} {
		params.Side = side
		for _, action := range []venue.ReduceAction{venue.ReduceActionClose, venue.ReduceActionUnwind, venue.ReduceActionEmergencyClose} {
			request, err := m.BuildReduce(venue.LiveReduceParams{LiveOrderParams: params, Action: action})
			want := "sell"
			if side == domain.SideShort {
				want = "buy"
			}
			if err != nil || request.Side != want || string(p.last.Action) != string(action) {
				t.Fatalf("reduce=%+v intent=%+v err=%v", request, p.last, err)
			}
		}
	}
	if m.Capabilities().LeverageUpdate != venue.LeverageUpdateNotRequired || m.Capabilities().ConfirmLeverageChange || m.Capabilities().ClientOrderLookup {
		t.Fatal("advertised unimplemented leverage/lookup capabilities")
	}
	if _, err := m.BuildLeverage(venue.LiveLeverageParams{}); err == nil {
		t.Fatal("built unsupported set-leverage action")
	}
}

func TestNativeClientOrderIDIsStableAndPreservesExactU64(t *testing.T) {
	first, err := NativeClientOrderID("orbital-l1open-123")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := NativeClientOrderID("orbital-l1open-123")
	other, _ := NativeClientOrderID("orbital-l1unwind-123")
	max, err := NativeClientOrderID("18446744073709551615")
	if first != again || first == other || err != nil || max != "18446744073709551615" {
		t.Fatal("native CID stability/precision lost")
	}
	for _, invalid := range []string{"", "0", "01", "18446744073709551616", " orbital-id"} {
		if _, err := NativeClientOrderID(invalid); err == nil {
			t.Fatal("accepted invalid CID", invalid)
		}
	}
}

func TestModuleHonorsCallerAndRuntimeCancellationBeforePreparation(t *testing.T) {
	p := &modulePreparer{}
	root, stop := context.WithCancel(context.Background())
	m, _ := NewLiveModule(root, p)
	params := venue.LiveOrderParams{Account: owner, Signer: agent, Symbol: "SOL", Side: domain.SideLong, Amount: 1, Price: 100, LimitPrice: 101, ClientOrderID: "42"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	params.Context = ctx
	if _, err := m.BuildOpen(params); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	params.Context = context.Background()
	stop()
	if _, err := m.BuildOpen(params); !errors.Is(err, context.Canceled) || p.calls != 0 {
		t.Fatal("cancelled module started preparation", err)
	}
}
