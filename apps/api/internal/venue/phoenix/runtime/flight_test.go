package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

func flightKeys() helper.FlightAccounts {
	key := func(n byte) string {
		bytes := make([]byte, 32)
		for i := range bytes {
			bytes[i] = n
		}
		return base58.Encode(bytes)
	}
	return helper.FlightAccounts{PhoenixProgramID: helper.PhoenixProgramID, FlightProgramID: helper.FlightProgramID,
		ProtocolRootAuthority: key(15), GlobalConfiguration: key(16), CollateralTransferPermission: key(17),
		BuilderAuthority: key(4), GlobalState: key(5), BuilderState: key(6), BuilderTraderAccount: key(7), CollateralTransferAuthority: key(8)}
}

type flightHelper struct {
	fakeHelper
	failure atomic.Bool
	block   atomic.Pointer[flightBlock]
}
type flightBlock struct {
	entered chan context.Context
	release chan struct{}
}

func (h *flightHelper) FlightAccounts(ctx context.Context, _ helper.FlightRequest) (helper.FlightAccounts, error) {
	if block := h.block.Load(); block != nil {
		block.entered <- ctx
		select {
		case <-block.release:
		case <-ctx.Done():
			return helper.FlightAccounts{}, ctx.Err()
		}
	}
	return flightKeys(), nil
}
func (h *flightHelper) VerifyFlight(context.Context, helper.VerifyFlightRequest) (helper.VerifiedFlight, error) {
	if h.failure.Load() {
		return helper.VerifiedFlight{}, errors.New("fee changed or inactive")
	}
	return helper.VerifiedFlight{Accounts: flightKeys(), FeeBPS: 2, Active: true}, nil
}

type flightChain struct {
	fakeChain
	minimum atomic.Uint64
	mode    atomic.Int32
	count   atomic.Int32
}

func (c *flightChain) AccountInfo(_ context.Context, address string, minSlot uint64, finalized bool) (rpc.AccountInfo, error) {
	if finalized {
		return rpc.AccountInfo{}, errors.New("expected confirmed read")
	}
	c.count.Add(1)
	c.minimum.Store(minSlot)
	owner := helper.FlightProgramID
	if address == flightKeys().BuilderTraderAccount || address == flightKeys().GlobalConfiguration || address == flightKeys().CollateralTransferPermission {
		owner = helper.PhoenixProgramID
	}
	slot := minSlot + 1
	switch c.mode.Load() {
	case 1:
		slot = minSlot - 1
	case 2:
		owner = "11111111111111111111111111111111"
	case 3:
		return rpc.AccountInfo{}, rpc.ErrAccountNotFound
	}
	return rpc.AccountInfo{Owner: owner, Slot: slot, DataBase64: "YQ=="}, nil
}

func flightRuntime(t *testing.T) (*Runtime, *flightHelper, *flightChain) {
	t.Helper()
	h, c := &flightHelper{}, &flightChain{}
	c.balance.Store(relayer.LowBalanceLamports)
	service, err := relayer.NewService(testSigner(t), c, relayer.Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := startRuntime(context.Background(), h, c, service, flightKeys().BuilderAuthority)
	t.Cleanup(r.Close)
	return r, h, c
}

func TestFlightOnlyVerifiesOnActionAndUsesEveryAccountAtMinimumSlot(t *testing.T) {
	r, h, c := flightRuntime(t)
	if status := r.FlightReadiness(); status.Ready || status.CheckedAt != nil || status.Reason != "action_verification_required" {
		t.Fatalf("startup should not claim on-chain verification: %+v", status)
	}
	if c.count.Load() != 0 || c.hashReads.Load() != 0 || c.reads.Load() != 0 {
		t.Fatal("startup performed idle chain reads")
	}
	proof, err := r.FlightProof(context.Background(), 150, flightKeys().ProtocolRootAuthority)
	if err != nil || proof.ProtocolRootAuthority != flightKeys().ProtocolRootAuthority || proof.FeeBPS != 2 || proof.Slot != 151 || c.minimum.Load() != 150 || c.count.Load() != 5 || proof.BuilderTraderAccount != flightKeys().BuilderTraderAccount {
		t.Fatalf("proof=%+v err=%v", proof, err)
	}
	h.failure.Store(true)
	if _, err := r.FlightProof(context.Background(), 150, flightKeys().ProtocolRootAuthority); err == nil {
		t.Fatal("cached success authorized changed registration")
	}
	if status := r.FlightReadiness(); status.Ready || status.Reason != "verification_unavailable" {
		t.Fatalf("failed verification stayed ready: %+v", status)
	}
	if !r.Readiness(context.Background()).Ready {
		t.Fatal("Flight failure disabled owner delegation infrastructure")
	}
	h.failure.Store(false)
	if _, err := r.FlightProof(context.Background(), 150, flightKeys().ProtocolRootAuthority); err != nil {
		t.Fatal(err)
	}
	if !r.FlightReadiness().Ready {
		t.Fatal("fresh registration did not restore readiness")
	}
}

func TestFlightWrongOwnerStaleSlotMissingAccountAndDiagnosticExpiry(t *testing.T) {
	r, _, c := flightRuntime(t)
	for _, mode := range []int32{1, 2, 3} {
		c.mode.Store(mode)
		if _, err := r.FlightProof(context.Background(), 150, flightKeys().ProtocolRootAuthority); err == nil {
			t.Fatalf("accepted invalid account mode %d", mode)
		}
	}
	c.mode.Store(0)
	if _, err := r.FlightProof(context.Background(), 150, flightKeys().ProtocolRootAuthority); err != nil {
		t.Fatal(err)
	}
	status := r.FlightReadiness()
	*status.CheckedAt = time.Now().Add(-time.Hour)
	if !r.FlightReadiness().Ready {
		t.Fatal("caller mutated stored readiness timestamp")
	}
	r.flightMu.Lock()
	at := time.Now().Add(-flightFreshness)
	r.flightStatus.CheckedAt = &at
	r.flightMu.Unlock()
	if status := r.FlightReadiness(); status.Ready || status.Reason != "verification_stale" {
		t.Fatalf("stale diagnostic: %+v", status)
	}
	r.Close()
	if status := r.FlightReadiness(); status.Ready || status.Reason != "stopped" {
		t.Fatalf("stopped diagnostic: %+v", status)
	}
}

func TestFlightCancellationDrainsSharedHelperAndDoesNotReadRPC(t *testing.T) {
	r, h, c := flightRuntime(t)
	block := &flightBlock{entered: make(chan context.Context, 1), release: make(chan struct{})}
	h.block.Store(block)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	before := c.count.Load()
	go func() { _, err := r.FlightProof(ctx, 100, flightKeys().ProtocolRootAuthority); done <- err }()
	helperCtx := <-block.entered
	cancel()
	if helperCtx.Err() != nil {
		t.Fatal("individual cancellation killed the helper frame")
	}
	close(block.release)
	if err := <-done; !errors.Is(err, context.Canceled) || c.count.Load() != before || h.closed.Load() != 0 {
		t.Fatalf("cancel err=%v reads=%d", err, c.count.Load()-before)
	}
}

// The public TS Flight account layouts and pinned Rust TraderHeader layout are
// decoded by the real helper, not a fake registration result or order manifest.
type flightDataChain struct {
	fakeChain
	accounts           helper.FlightAccounts
	fee                atomic.Uint64
	wrongRoot          atomic.Bool
	permissionDisabled atomic.Bool
}

func (c *flightDataChain) AccountInfo(_ context.Context, address string, minimum uint64, _ bool) (rpc.AccountInfo, error) {
	owner, name, size := helper.FlightProgramID, "global_state", 368
	if address == c.accounts.BuilderState {
		name, size = "builder_state", 216
	} else if address == c.accounts.BuilderTraderAccount {
		owner, name, size = helper.PhoenixProgramID, "trader", 224
	} else if address == c.accounts.GlobalConfiguration {
		owner, name, size = helper.PhoenixProgramID, "global_configuration", 1104
	} else if address == c.accounts.CollateralTransferPermission {
		owner, name, size = helper.PhoenixProgramID, "permission_account", 168
	} else if address != c.accounts.GlobalState {
		return rpc.AccountInfo{}, rpc.ErrAccountNotFound
	}
	data := make([]byte, size)
	digest := sha256.Sum256([]byte("account:" + name))
	copy(data, digest[:8])
	pubkey := func(offset int, key string) { decoded, _ := base58.Decode(key); copy(data[offset:offset+32], decoded) }
	switch name {
	case "global_configuration":
		pubkey(8, c.accounts.GlobalConfiguration)
		pubkey(40, c.accounts.ProtocolRootAuthority)
		if c.wrongRoot.Load() {
			pubkey(40, c.accounts.BuilderAuthority)
		}
	case "permission_account":
		pubkey(8, c.accounts.ProtocolRootAuthority)
		pubkey(40, c.accounts.CollateralTransferAuthority)
		if !c.permissionDisabled.Load() {
			binary.LittleEndian.PutUint64(data[80:88], 1)
		}
		binary.LittleEndian.PutUint64(data[96:104], ^uint64(0))
	case "global_state":
		pubkey(8, helper.PhoenixProgramID)
		binary.LittleEndian.PutUint64(data[104:112], 100)
	case "builder_state":
		pubkey(8, c.accounts.BuilderAuthority)
		pubkey(40, c.accounts.BuilderTraderAccount)
		binary.LittleEndian.PutUint64(data[72:80], 1)
		binary.LittleEndian.PutUint64(data[80:88], c.fee.Load())
	case "trader":
		// Discriminator 8 + SequenceNumber 16 + key 32.
		pubkey(56, c.accounts.BuilderAuthority)
	}
	return rpc.AccountInfo{Owner: owner, Slot: minimum, DataBase64: base64.StdEncoding.EncodeToString(data)}, nil
}

func TestFlightProofWithPinnedHelperRejectsInvalidChainRegistrationAndPermission(t *testing.T) {
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set ORBITAL_PHOENIX_HELPER_TEST_PATH to built phoenix-helper")
	}
	h, err := helper.Start(context.Background(), path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	accounts, err := h.FlightAccounts(context.Background(), helper.FlightRequest{BuilderAuthority: flightKeys().BuilderAuthority, ProtocolRootAuthority: flightKeys().ProtocolRootAuthority})
	if err != nil {
		t.Fatal(err)
	}
	c := &flightDataChain{accounts: accounts}
	c.fee.Store(2)
	c.balance.Store(relayer.LowBalanceLamports)
	service, err := relayer.NewService(testSigner(t), c, relayer.Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := startRuntime(context.Background(), h, c, service, accounts.BuilderAuthority)
	t.Cleanup(r.Close)
	if status := r.FlightReadiness(); status.Ready || status.CheckedAt != nil {
		t.Fatalf("real helper startup unexpectedly verified chain: %+v", status)
	}
	proof, err := r.FlightProof(context.Background(), 155, flightKeys().ProtocolRootAuthority)
	if err != nil || proof.Slot != 155 || proof.BuilderState != accounts.BuilderState || proof.FeeBPS != 2 {
		t.Fatalf("real helper proof=%+v err=%v", proof, err)
	}
	c.fee.Store(3)
	if _, err := r.FlightProof(context.Background(), 155, flightKeys().ProtocolRootAuthority); err == nil {
		t.Fatal("actual chain fee 3 bps accepted because manifest said 2 bps")
	}
	if r.FlightReadiness().Ready || !r.Readiness(context.Background()).Ready {
		t.Fatal("fee change must block Flight without blocking delegation")
	}
	c.fee.Store(2)
	c.wrongRoot.Store(true)
	if _, err := r.FlightProof(context.Background(), 155, accounts.ProtocolRootAuthority); err == nil {
		t.Fatal("metadata root differs from canonical chain config")
	}
	c.wrongRoot.Store(false)
	c.permissionDisabled.Store(true)
	if _, err := r.FlightProof(context.Background(), 155, accounts.ProtocolRootAuthority); err == nil {
		t.Fatal("disabled collateral transfer permission accepted")
	}
}
