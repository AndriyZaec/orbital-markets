package relayer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/mr-tron/base58"
)

func fixtureKey(value byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{value}, ed25519.SeedSize))
}

func fixtureSigner(t *testing.T) *Signer {
	t.Helper()
	signer, err := NewSigner(base58.Encode(fixtureKey(1)))
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func setMessage(built *helper.BuildOrderResult, message []byte) {
	digest := sha256.Sum256(message)
	built.MessageBase64 = base64.StdEncoding.EncodeToString(message)
	built.MessageSHA256 = hex.EncodeToString(digest[:])
}

func fixtureOrder(t *testing.T) (helper.BuildOrderResult, []byte, ed25519.PrivateKey) {
	t.Helper()
	signer, agent := fixtureSigner(t), fixtureKey(2)
	feePayer, _ := base58.Decode(signer.Address())
	program, _ := base58.Decode(helper.PhoenixProgramID)
	hash := bytes.Repeat([]byte{9}, 32)
	message := []byte{2, 1, 1, 3} // two signers, agent readonly, program readonly
	message = append(message, feePayer...)
	message = append(message, agent[32:]...)
	message = append(message, program...)
	message = append(message, hash...)
	message = append(message, 1, 2, 1, 1, 1, 42) // one compiled instruction
	built := helper.BuildOrderResult{
		Manifest: helper.OrderManifest{ComputeBudget: (helper.ComputeBudget{}).Effective()},
		RequiredSigners: []helper.RequiredSigner{
			{Index: 0, Pubkey: signer.Address(), Role: "fee_payer"},
			{Index: 1, Pubkey: base58.Encode(agent[32:]), Role: "position_authority"},
		},
		RecentBlockhash: base58.Encode(hash), LastValidBlockHeight: 150, MetadataGeneration: 1,
	}
	setMessage(&built, message)
	return built, message, agent
}

func TestKeyLoadingValidatesSolanaKeypair(t *testing.T) {
	key := fixtureKey(1)
	signer, err := NewSigner(" \n" + base58.Encode(key) + "\n")
	if err != nil || signer.Address() != base58.Encode(key[32:]) {
		t.Fatalf("address mismatch err=%v", err)
	}
	corrupt := append([]byte(nil), key...)
	corrupt[63] ^= 1
	for _, secret := range []string{"", "invalid-secret-0", base58.Encode(key[:32]), base58.Encode(corrupt), strings.Repeat("a", 100)} {
		if signer, err := NewSigner(secret); signer != nil || !errors.Is(err, ErrInvalidKey) {
			t.Fatal("invalid relayer key accepted")
		} else if secret != "" && strings.Contains(err.Error(), secret) {
			t.Fatal("secret leaked in error")
		}
	}
}

func verifyPacket(t *testing.T, signer *Signer, agent ed25519.PrivateKey, message, packet []byte) {
	t.Helper()
	if len(packet) != 129+len(message) || packet[0] != 2 || !bytes.Equal(packet[129:], message) {
		t.Fatal("co-signing changed message bytes or signature framing")
	}
	pub, _ := base58.Decode(signer.Address())
	if !ed25519.Verify(pub, message, packet[1:65]) || !ed25519.Verify(ed25519.PublicKey(agent[32:]), message, packet[65:129]) {
		t.Fatal("transaction does not contain both valid signatures in signer order")
	}
}

func TestCoSignPreservesExactMessageAndAgentSignature(t *testing.T) {
	signer := fixtureSigner(t)
	built, message, agent := fixtureOrder(t)
	sig := ed25519.Sign(agent, message)
	before := built.MessageBase64
	packet, err := signer.CoSign(built, base58.Encode(agent[32:]), base58.Encode(sig))
	if err != nil {
		t.Fatal(err)
	}
	verifyPacket(t, signer, agent, message, packet)
	if !bytes.Equal(packet[65:129], sig) || built.MessageBase64 != before {
		t.Fatal("stored data mutated")
	}
}

func TestCoSignRejectsMessageAndSignerTampering(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*helper.BuildOrderResult, []byte, *string, *string)
		want   error
	}{
		{"corrupt base64", func(b *helper.BuildOrderResult, _ []byte, _, _ *string) { b.MessageBase64 = "!" }, ErrInvalidMessage},
		{"wrong digest", func(b *helper.BuildOrderResult, _ []byte, _, _ *string) { b.MessageSHA256 = strings.Repeat("0", 64) }, ErrInvalidMessage},
		{"changed instruction with updated digest", func(b *helper.BuildOrderResult, m []byte, _, _ *string) { m[len(m)-1] ^= 1; setMessage(b, m) }, ErrInvalidSignature},
		{"wrong expected agent", func(_ *helper.BuildOrderResult, _ []byte, a, _ *string) { *a = base58.Encode(fixtureKey(3)[32:]) }, ErrSignerMismatch},
		{"wrong signing key", func(_ *helper.BuildOrderResult, m []byte, _, s *string) {
			*s = base58.Encode(ed25519.Sign(fixtureKey(3), m))
		}, ErrInvalidSignature},
		{"short signature", func(_ *helper.BuildOrderResult, _ []byte, _, s *string) { *s = base58.Encode([]byte{1}) }, ErrInvalidSignature},
		{"wrong fee payer in wire", func(b *helper.BuildOrderResult, m []byte, _, _ *string) { m[4] ^= 1; setMessage(b, m) }, ErrSignerMismatch},
		{"wrong agent in wire", func(b *helper.BuildOrderResult, m []byte, _, _ *string) { m[36] ^= 1; setMessage(b, m) }, ErrSignerMismatch},
		{"wrong signer index", func(b *helper.BuildOrderResult, _ []byte, _, _ *string) { b.RequiredSigners[1].Index = 0 }, ErrSignerMismatch},
		{"extra signer metadata", func(b *helper.BuildOrderResult, _ []byte, _, _ *string) {
			b.RequiredSigners = append(b.RequiredSigners, helper.RequiredSigner{})
		}, ErrSignerMismatch},
		{"wrong blockhash metadata", func(b *helper.BuildOrderResult, _ []byte, _, _ *string) {
			b.RecentBlockhash = base58.Encode(make([]byte, 32))
		}, ErrInvalidMessage},
		{"versioned message", func(b *helper.BuildOrderResult, m []byte, _, _ *string) { m[0] = 128; setMessage(b, m) }, ErrInvalidMessage},
		{"extra wire signer", func(b *helper.BuildOrderResult, m []byte, _, _ *string) { m[0] = 3; setMessage(b, m) }, ErrInvalidMessage},
		{"readonly fee payer", func(b *helper.BuildOrderResult, m []byte, _, _ *string) { m[1] = 2; setMessage(b, m) }, ErrInvalidMessage},
		{"truncated keys", func(b *helper.BuildOrderResult, m []byte, _, _ *string) { setMessage(b, m[:50]) }, ErrInvalidMessage},
		{"truncated blockhash", func(b *helper.BuildOrderResult, m []byte, _, _ *string) { setMessage(b, m[:110]) }, ErrInvalidMessage},
		{"noncanonical account count", func(b *helper.BuildOrderResult, m []byte, _, _ *string) {
			m[3] = 131
			m = append(m[:4], append([]byte{0}, m[4:]...)...)
			setMessage(b, m)
		}, ErrInvalidMessage},
		{"oversized packet", func(b *helper.BuildOrderResult, _ []byte, _, _ *string) {
			setMessage(b, make([]byte, maxMessageSize+1))
		}, ErrInvalidMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			built, message, agent := fixtureOrder(t)
			address, signature := base58.Encode(agent[32:]), base58.Encode(ed25519.Sign(agent, message))
			tc.change(&built, message, &address, &signature)
			packet, err := fixtureSigner(t).CoSign(built, address, signature)
			if packet != nil || !errors.Is(err, tc.want) {
				t.Fatalf("packet returned=%t err=%v want=%v", packet != nil, err, tc.want)
			}
		})
	}
}

// Explicit opt-in uses the pinned local Rust helper; never RPC or real accounts.
func TestCoSignPinnedHelperOrders(t *testing.T) {
	path := os.Getenv("ORBITAL_PHOENIX_HELPER_TEST_PATH")
	if path == "" {
		t.Skip("set ORBITAL_PHOENIX_HELPER_TEST_PATH to built phoenix-helper")
	}
	ctx := context.Background()
	client, err := helper.Start(ctx, path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	signer, agent := fixtureSigner(t), fixtureKey(2)
	for _, action := range []helper.Action{helper.ActionOpen, helper.ActionClose, helper.ActionUnwind, helper.ActionEmergencyClose} {
		t.Run(string(action), func(t *testing.T) {
			request := helper.BuildOrderRequest{
				Action: action, OwnerAuthority: base58.Encode(fixtureKey(3)[32:]), PositionAuthority: base58.Encode(agent[32:]), FeePayer: signer.Address(),
				Symbol: "SOL", Side: helper.SideBid, NumBaseLots: 100, PriceLimitTicks: 12345,
				ReduceOnly: action != helper.ActionOpen, ClientOrderID: 42, LastValidSlot: 123456,
				RecentBlockhash: base58.Encode(bytes.Repeat([]byte{9}, 32)), LastValidBlockHeight: 999999, MetadataGeneration: 7,
				Accounts: helper.MarketAccounts{
					PerpAssetMap: "PhUsd11YkbjSaWjFncfAAmatntsjx3MgDR9B6g1ks3A", Orderbook: "EMBERpYNE6ehWmXymZZS2skiFmCa9V5dp14e1iduM5qy",
					SplineCollection: "RiSeVw3ZjNfsaXPRb4mgaqYaEEt41pNNJoDvVh7pgQj", GlobalTraderIndex: []string{helper.FlightProgramID}, ActiveTraderBuffer: []string{"phDEVv4w6BcfkLrLNeXr8HhhgQxnxziVGXpGPcaadMf"},
				},
			}
			if action == helper.ActionOpen || action == helper.ActionClose {
				request.BuilderAuthority = "Vote111111111111111111111111111111111111111"
			}
			built, err := client.BuildOrder(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			message, _ := base64.StdEncoding.DecodeString(built.MessageBase64)
			packet, err := signer.CoSign(built, request.PositionAuthority, base58.Encode(ed25519.Sign(agent, message)))
			if err != nil {
				t.Fatal(err)
			}
			verifyPacket(t, signer, agent, message, packet)
		})
	}
}
