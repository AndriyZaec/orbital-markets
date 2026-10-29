package relayer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

type testChain struct {
	height  func(context.Context, uint64) (uint64, error)
	fee     func(context.Context, []byte, uint64) (uint64, error)
	balance func(context.Context, string) (uint64, error)
}

func (c testChain) BlockHeight(ctx context.Context, slot uint64) (uint64, error) {
	return c.height(ctx, slot)
}
func (c testChain) FeeForMessage(ctx context.Context, m []byte, slot uint64) (uint64, error) {
	return c.fee(ctx, m, slot)
}
func (c testChain) Balance(ctx context.Context, a string) (uint64, error) { return c.balance(ctx, a) }

func TestCoSignFeeBalanceAndExpiryGate(t *testing.T) {
	rpcFailure := errors.New("RPC unavailable")
	for _, tc := range []struct {
		name         string
		fee, balance uint64
		heights      []uint64
		failAt       string
		want         error
		wantCalls    []string
	}{
		{"at fee cap and exact balance", 10000, 10000, []uint64{150, 150}, "", nil, []string{"height", "fee", "balance", "height"}},
		{"zero balance", 10000, 0, []uint64{149}, "", ErrInsufficientBalance, []string{"height", "fee", "balance"}},
		{"fee exceeds cap", 10001, LowBalanceLamports, []uint64{149}, "", ErrFeeCap, []string{"height", "fee"}},
		{"zero fee rejected", 0, LowBalanceLamports, []uint64{149}, "", rpc.ErrInvalidResponse, []string{"height", "fee"}},
		{"expired upfront", 10000, LowBalanceLamports, []uint64{151}, "", rpc.ErrExpired, []string{"height"}},
		{"expired during fee checks", 10000, LowBalanceLamports, []uint64{149, 151}, "", rpc.ErrExpired, []string{"height", "fee", "balance", "height"}},
		{"height unavailable", 10000, LowBalanceLamports, nil, "height", rpcFailure, []string{"height"}},
		{"fee unavailable", 10000, LowBalanceLamports, []uint64{149}, "fee", rpcFailure, []string{"height", "fee"}},
		{"balance unavailable", 10000, LowBalanceLamports, []uint64{149}, "balance", rpcFailure, []string{"height", "fee", "balance"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signer := fixtureSigner(t)
			built, message, agent := fixtureOrder(t)
			var calls []string
			heightIndex := 0
			chain := testChain{
				height: func(ctx context.Context, slot uint64) (uint64, error) {
					calls = append(calls, "height")
					if slot != 100 {
						t.Errorf("min slot=%d", slot)
					}
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 15*time.Second {
						t.Error("unbounded gate RPC")
					}
					if tc.failAt == "height" {
						return 0, rpcFailure
					}
					if heightIndex >= len(tc.heights) {
						t.Error("unexpected height read")
						return 0, rpcFailure
					}
					h := tc.heights[heightIndex]
					heightIndex++
					return h, nil
				},
				fee: func(_ context.Context, m []byte, slot uint64) (uint64, error) {
					calls = append(calls, "fee")
					if !bytes.Equal(m, message) || slot != 100 {
						t.Error("fee quote changed message/context")
					}
					if tc.failAt == "fee" {
						return 0, rpcFailure
					}
					return tc.fee, nil
				},
				balance: func(_ context.Context, address string) (uint64, error) {
					calls = append(calls, "balance")
					if address != signer.Address() {
						t.Error("wrong fee payer balance")
					}
					if tc.failAt == "balance" {
						return 0, rpcFailure
					}
					return tc.balance, nil
				},
			}
			service, err := NewService(signer, chain, Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			packet, err := service.CoSign(context.Background(), built, base58.Encode(agent[32:]), base58.Encode(ed25519.Sign(agent, message)), 100)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Fatalf("err=%v calls=%v", err, calls)
			}
			if err == nil {
				verifyPacket(t, signer, agent, message, packet)
			} else if packet != nil {
				t.Fatal("blocked gate returned signed packet")
			}
		})
	}
}

func TestBalanceStatusUsesFreshReadsAndWarningBoundary(t *testing.T) {
	rpcFailure := errors.New("balance unavailable")
	var balance uint64 = LowBalanceLamports
	var fail bool
	service, err := NewService(fixtureSigner(t), testChain{balance: func(context.Context, string) (uint64, error) {
		if fail {
			return 0, rpcFailure
		}
		return balance, nil
	}}, Policy{MaxTransactionFeeLamports: 10000}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.CheckBalance(context.Background())
	if err != nil || status.Low || status.Lamports != balance || status.ObservedAt.IsZero() {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	balance--
	status, err = service.CheckBalance(context.Background())
	if err != nil || !status.Low || status.Lamports != balance {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	fail = true
	status, err = service.CheckBalance(context.Background())
	if !errors.Is(err, rpcFailure) || !status.ObservedAt.IsZero() {
		t.Fatalf("reused successful balance: %+v %v", status, err)
	}
}

type cancelLogWriter struct {
	buffer bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelLogWriter) Write(p []byte) (int, error) {
	n, err := w.buffer.Write(p)
	w.cancel()
	return n, err
}

func TestMonitorLogsLowOrUnavailableBalanceAndStops(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		writer := &cancelLogWriter{cancel: cancel}
		calls := 0
		service, err := NewService(fixtureSigner(t), testChain{balance: func(context.Context, string) (uint64, error) {
			calls++
			if unavailable {
				return 0, errors.New("RPC unavailable")
			}
			return 0, nil
		}}, Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(writer, nil)))
		if err != nil {
			t.Fatal(err)
		}
		service.Run(ctx)
		cancel()
		want := "SOL balance low"
		if unavailable {
			want = "balance unavailable"
		}
		if calls != 1 || !strings.Contains(writer.buffer.String(), want) {
			t.Fatalf("calls=%d log=%s", calls, writer.buffer.String())
		}
		service.Run(ctx)
		if calls != 1 {
			t.Fatal("cancelled monitor started network work")
		}
	}
}

func TestGateRejectsInvalidInputBeforeNetwork(t *testing.T) {
	signer := fixtureSigner(t)
	chain := testChain{}
	if _, err := NewService(signer, chain, Policy{}, slog.Default()); err == nil {
		t.Fatal("missing fee cap accepted")
	}
	service, err := NewService(signer, chain, Policy{MaxTransactionFeeLamports: 10000}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	built, message, agent := fixtureOrder(t)
	address, signature := base58.Encode(agent[32:]), base58.Encode(ed25519.Sign(agent, message))
	if packet, err := service.CoSign(context.Background(), built, address, "bad", 100); packet != nil || !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("invalid signature err=%v", err)
	}
	built.LastValidBlockHeight = 0
	if packet, err := service.CoSign(context.Background(), built, address, signature, 100); packet != nil || !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("missing expiry err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if packet, err := service.CoSign(ctx, built, address, signature, 100); packet != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
}
