package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
)

type accountHelper struct {
	fakeHelper
	build   helper.BuildDelegationRequest
	entered chan context.Context
	release chan struct{}
}

func (h *accountHelper) BuildDelegation(_ context.Context, q helper.BuildDelegationRequest) (helper.BuildDelegationResult, error) {
	h.build = q
	return helper.BuildDelegationResult{TraderAccount: "trader"}, nil
}
func (h *accountHelper) DecodeTraderAuthority(context.Context, helper.DecodeAuthorityRequest) (helper.AuthorityResult, error) {
	return helper.AuthorityResult{PositionAuthority: "So11111111111111111111111111111111111111112"}, nil
}
func (h *accountHelper) ComputeRisk(ctx context.Context, _ helper.RiskRequest) (helper.RiskResult, error) {
	h.entered <- ctx
	select {
	case <-h.release:
		return helper.RiskResult{}, nil
	case <-ctx.Done():
		return helper.RiskResult{}, ctx.Err()
	}
}

type accountChain struct {
	fakeChain
	minimum uint64
	program string
}

func (c *accountChain) AccountInfo(_ context.Context, trader string, minSlot uint64, finalized bool) (rpc.AccountInfo, error) {
	c.minimum = minSlot
	return rpc.AccountInfo{Owner: c.program, Slot: minSlot, DataBase64: "YQ=="}, nil
}

func accountRuntime(t *testing.T, h *accountHelper, c *accountChain) *Runtime {
	t.Helper()
	service, err := relayer.NewService(testSigner(t), c, relayer.Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := startRuntime(context.Background(), h, c, service)
	t.Cleanup(r.Close)
	return r
}

func TestAccountAuthorityUsesOwnerPDAAndMinimumAccountSlot(t *testing.T) {
	h := &accountHelper{}
	c := &accountChain{program: helper.PhoenixProgramID}
	r := accountRuntime(t, h, c)
	owner := "11111111111111111111111111111111"
	a, err := r.AccountAuthority(context.Background(), owner, 150)
	if err != nil || a.Slot != 150 || c.minimum != 150 || h.build.OwnerAuthority != owner || h.build.NewPositionAuthority != owner || h.build.Action != helper.ActionRevoke {
		t.Fatalf("authority=%+v request=%+v err=%v", a, h.build, err)
	}
	c.program = owner
	if _, err := r.AccountAuthority(context.Background(), owner, 150); !errors.Is(err, rpc.ErrInvalidResponse) {
		t.Fatal("accepted non-Phoenix program ownership", err)
	}
}

func TestAccountEvictionCancellationDrainsSharedHelperFrame(t *testing.T) {
	h := &accountHelper{entered: make(chan context.Context, 1), release: make(chan struct{})}
	r := accountRuntime(t, h, &accountChain{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := r.ComputeRisk(ctx, helper.RiskRequest{}); done <- err }()
	helperCtx := <-h.entered
	cancel()
	if helperCtx.Err() != nil {
		t.Fatal("account cancellation was forwarded to shared NDJSON frame")
	}
	close(h.release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("bounded read did not finish")
	}
	if h.closed.Load() != 0 {
		t.Fatal("account cancellation stopped shared helper")
	}
	if _, err := r.helper.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
}
