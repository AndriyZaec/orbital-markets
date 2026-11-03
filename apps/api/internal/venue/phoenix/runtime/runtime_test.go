package runtime

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

type fakeHelper struct {
	closed atomic.Int32
	fail   bool
}

func (h *fakeHelper) Health(ctx context.Context) (helper.HealthResult, error) {
	if ctx.Err() != nil {
		return helper.HealthResult{}, ctx.Err()
	}
	if h.fail {
		return helper.HealthResult{}, errors.New("helper down")
	}
	return helper.HealthResult{HelperVersion: "0.1.0", PhoenixRiseVersion: helper.PhoenixRiseVersion}, nil
}
func (h *fakeHelper) Close() { h.closed.Add(1) }
func (h *fakeHelper) ComputeRisk(context.Context, helper.RiskRequest) (helper.RiskResult, error) {
	return helper.RiskResult{}, errors.New("unused")
}
func (h *fakeHelper) BuildDelegation(context.Context, helper.BuildDelegationRequest) (helper.BuildDelegationResult, error) {
	return helper.BuildDelegationResult{}, errors.New("unused")
}
func (h *fakeHelper) DecodeTraderAuthority(context.Context, helper.DecodeAuthorityRequest) (helper.AuthorityResult, error) {
	return helper.AuthorityResult{}, errors.New("unused")
}

type fakeChain struct {
	balance atomic.Uint64
	failure atomic.Int32
	reads   atomic.Int32
}

func (c *fakeChain) AccountInfo(context.Context, string, uint64, bool) (rpc.AccountInfo, error) {
	return rpc.AccountInfo{}, errors.New("unused")
}
func (c *fakeChain) SimulateAndSend(context.Context, []byte, uint64, uint64) (string, error) {
	return "", errors.New("unused")
}
func (c *fakeChain) SignatureStatus(context.Context, string) (*rpc.SignatureStatus, error) {
	return nil, errors.New("unused")
}
func (c *fakeChain) FinalizedBlockhashValidity(context.Context, string, uint64) (rpc.BlockhashValidity, error) {
	return rpc.BlockhashValidity{}, errors.New("unused")
}

func (c *fakeChain) BlockHeight(context.Context, uint64) (uint64, error)           { return 100, nil }
func (c *fakeChain) FeeForMessage(context.Context, []byte, uint64) (uint64, error) { return 10000, nil }
func (c *fakeChain) LatestBlockhash(ctx context.Context) (rpc.Blockhash, error) {
	if ctx.Err() != nil {
		return rpc.Blockhash{}, ctx.Err()
	}
	if c.failure.Load() == 1 {
		return rpc.Blockhash{}, errors.New("RPC down")
	}
	return rpc.Blockhash{Hash: base58.Encode(make([]byte, 32)), LastValidBlockHeight: 150, Slot: 100}, nil
}
func (c *fakeChain) Balance(ctx context.Context, _ string) (uint64, error) {
	c.reads.Add(1)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if c.failure.Load() == 2 {
		return 0, errors.New("balance down")
	}
	return c.balance.Load(), nil
}

func testSigner(t *testing.T) *relayer.Signer {
	t.Helper()
	signer, err := relayer.NewSigner(base58.Encode(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))))
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func testRuntime(t *testing.T, ctx context.Context, h *fakeHelper, c *fakeChain) *Runtime {
	t.Helper()
	service, err := relayer.NewService(testSigner(t), c, relayer.Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := startRuntime(ctx, h, c, service, 10000)
	t.Cleanup(r.Close)
	return r
}

func TestInfrastructureReadinessUsesCurrentComponents(t *testing.T) {
	c := &fakeChain{}
	c.balance.Store(relayer.LowBalanceLamports)
	r := testRuntime(t, context.Background(), &fakeHelper{}, c)
	if status := r.Readiness(context.Background()); !status.Ready || status.BalanceLow {
		t.Fatalf("healthy status=%+v", status)
	}
	c.balance.Store(10000)
	if status := r.Readiness(context.Background()); !status.Ready || !status.BalanceLow {
		t.Fatalf("affordable low balance=%+v", status)
	}
	c.balance.Store(9999)
	if status := r.Readiness(context.Background()); status.Ready || status.Reason != "insufficient_fee_budget" {
		t.Fatalf("insufficient status=%+v", status)
	}
	c.balance.Store(relayer.LowBalanceLamports)
	c.failure.Store(2)
	if status := r.Readiness(context.Background()); status.Ready || status.Reason != "balance_unavailable" {
		t.Fatalf("balance status=%+v", status)
	}
	c.failure.Store(1)
	if status := r.Readiness(context.Background()); status.Ready || status.Reason != "rpc_unavailable" {
		t.Fatalf("RPC status=%+v", status)
	}
	c.failure.Store(0)
	if status := r.Readiness(context.Background()); !status.Ready {
		t.Fatalf("did not recover after fresh reads: %+v", status)
	}
}

func TestFailedHelperAndShutdownAreUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, c := &fakeHelper{fail: true}, &fakeChain{}
	r := testRuntime(t, ctx, h, c)
	if status := r.Readiness(context.Background()); status.Ready || status.Reason != "helper_unavailable" {
		t.Fatalf("helper status=%+v", status)
	}
	cancel()
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop on parent cancellation")
	}
	r.Close()
	r.Close()
	if h.closed.Load() != 1 {
		t.Fatalf("helper close count=%d", h.closed.Load())
	}
	reads := c.reads.Load()
	if status := r.Readiness(context.Background()); status.Ready || status.Reason != "stopped" {
		t.Fatalf("shutdown status=%+v", status)
	}
	if c.reads.Load() != reads {
		t.Fatal("shutdown readiness started network work")
	}
}

func TestStartRejectsInvalidConfigurationBeforeHelperLaunch(t *testing.T) {
	config := Config{HelperPath: "/does-not-exist/phoenix-helper", RPCURL: "https://rpc.example", RelayerPrivateKey: base58.Encode(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))), MaxTransactionFeeLamports: 10000}
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"key", func(c *Config) { c.RelayerPrivateKey = "invalid-secret" }},
		{"URL", func(c *Config) { c.RPCURL = "invalid" }},
		{"cap", func(c *Config) { c.MaxTransactionFeeLamports = 0 }},
		{"helper", func(c *Config) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config
			tc.change(&c)
			if r, err := Start(context.Background(), c, slog.Default()); err == nil || r != nil {
				t.Fatal("invalid config started runtime")
			}
		})
	}
}

// Uses the pinned local helper with a mock RPC server; no real RPC or signing.
func TestRuntimeWithPinnedHelperAndRPC(t *testing.T) {
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set ORBITAL_PHOENIX_HELPER_TEST_PATH to built phoenix-helper")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var value any
		switch request.Method {
		case "getLatestBlockhash":
			value = map[string]any{"blockhash": base58.Encode(make([]byte, 32)), "lastValidBlockHeight": 150}
		case "getBalance":
			value = uint64(relayer.LowBalanceLamports)
		default:
			t.Errorf("unexpected RPC method: %s", request.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"context": map[string]uint64{"slot": 100}, "value": value}})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := Start(ctx, Config{HelperPath: path, RPCURL: server.URL, RelayerPrivateKey: base58.Encode(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))), MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if status := r.Readiness(ctx); !status.Ready {
		t.Fatalf("runtime status=%+v", status)
	}
	cancel()
	r.Close()
	if _, err := r.helper.Health(context.Background()); err == nil {
		t.Fatal("shutdown left helper serving requests")
	}
}
