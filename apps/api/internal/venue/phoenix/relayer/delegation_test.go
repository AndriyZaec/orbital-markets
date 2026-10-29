package relayer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

func delegationFixture(t *testing.T) (helper.BuildDelegationResult, []byte, ed25519.PrivateKey) {
	order, message, owner := fixtureOrder(t)
	order.RequiredSigners[1].Role = "owner_authority"
	return helper.BuildDelegationResult{MessageBase64: order.MessageBase64, MessageSHA256: order.MessageSHA256, RecentBlockhash: order.RecentBlockhash, LastValidBlockHeight: order.LastValidBlockHeight, RequiredSigners: order.RequiredSigners}, message, owner
}

func TestDelegationRequiresExactOwnerSignature(t *testing.T) {
	built, message, owner := delegationFixture(t)
	signer := fixtureSigner(t)
	address := base58.Encode(owner[32:])
	signature := base58.Encode(ed25519.Sign(owner, message))
	packet, err := signer.CoSignDelegation(built, address, signature)
	if err != nil {
		t.Fatal(err)
	}
	verifyPacket(t, signer, owner, message, packet)
	if packet, err := signer.CoSignDelegation(built, address, base58.Encode(ed25519.Sign(fixtureKey(3), message))); packet != nil || !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("delegate signed owner action: %v", err)
	}
	if packet, err := signer.CoSignDelegation(built, base58.Encode(fixtureKey(3)[32:]), signature); packet != nil || !errors.Is(err, ErrSignerMismatch) {
		t.Fatalf("wrong bound owner accepted: %v", err)
	}
	built.RequiredSigners[1].Role = "position_authority"
	if packet, err := signer.CoSignDelegation(built, address, signature); packet != nil || !errors.Is(err, ErrSignerMismatch) {
		t.Fatalf("agent role accepted: %v", err)
	}
}

func TestDelegationUsesFeeBalanceAndExpiryGate(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		fee, balance, height uint64
		want                 error
	}{
		{"accepted", 10000, 10000, 150, nil},
		{"fee cap", 10001, 10000, 150, ErrFeeCap},
		{"balance", 10000, 9999, 150, ErrInsufficientBalance},
		{"expired", 10000, 10000, 151, rpc.ErrExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			built, message, owner := delegationFixture(t)
			signer := fixtureSigner(t)
			chain := testChain{
				height:  func(context.Context, uint64) (uint64, error) { return tc.height, nil },
				fee:     func(context.Context, []byte, uint64) (uint64, error) { return tc.fee, nil },
				balance: func(context.Context, string) (uint64, error) { return tc.balance, nil },
			}
			service, err := NewService(signer, chain, Policy{MaxTransactionFeeLamports: 10000}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			packet, err := service.CoSignDelegation(context.Background(), built, base58.Encode(owner[32:]), base58.Encode(ed25519.Sign(owner, message)), 100)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			if err == nil {
				verifyPacket(t, signer, owner, message, packet)
			} else if packet != nil {
				t.Fatal("failed gate returned packet")
			}
		})
	}
}

func TestCoSignPinnedHelperDelegationAndRevoke(t *testing.T) {
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set ORBITAL_PHOENIX_HELPER_TEST_PATH to built phoenix-helper")
	}
	client, err := helper.Start(context.Background(), path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	signer, owner, agent := fixtureSigner(t), fixtureKey(2), fixtureKey(3)
	var trader string
	for _, action := range []helper.DelegationAction{helper.ActionDelegate, helper.ActionRevoke} {
		t.Run(string(action), func(t *testing.T) {
			target := base58.Encode(agent[32:])
			if action == helper.ActionRevoke {
				target = base58.Encode(owner[32:])
			}
			built, err := client.BuildDelegation(context.Background(), helper.BuildDelegationRequest{Action: action, OwnerAuthority: base58.Encode(owner[32:]), NewPositionAuthority: target, FeePayer: signer.Address(), RecentBlockhash: base58.Encode(make([]byte, 32)), LastValidBlockHeight: 150})
			if err != nil {
				t.Fatal(err)
			}
			if trader != "" && trader != built.TraderAccount {
				t.Fatal("revoke changed owner-derived trader")
			}
			trader = built.TraderAccount
			message, _ := base64.StdEncoding.DecodeString(built.MessageBase64)
			packet, err := signer.CoSignDelegation(built, base58.Encode(owner[32:]), base58.Encode(ed25519.Sign(owner, message)))
			if err != nil {
				t.Fatal(err)
			}
			verifyPacket(t, signer, owner, message, packet)
			if packet, err := signer.CoSignDelegation(built, base58.Encode(owner[32:]), base58.Encode(ed25519.Sign(agent, message))); packet != nil || !errors.Is(err, ErrInvalidSignature) {
				t.Fatal("new agent authorized its own delegation")
			}
		})
	}
}
