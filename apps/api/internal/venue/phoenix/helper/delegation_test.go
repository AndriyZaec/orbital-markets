package helper

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"
)

func fakeDelegationRequest() BuildDelegationRequest {
	return BuildDelegationRequest{Action: ActionDelegate, OwnerAuthority: "owner", NewPositionAuthority: "agent", FeePayer: "payer", RecentBlockhash: "hash", LastValidBlockHeight: 150}
}

func fakeDelegationResult(req BuildDelegationRequest) BuildDelegationResult {
	message := []byte("delegation-message")
	digest := sha256.Sum256(message)
	return BuildDelegationResult{
		MessageBase64: base64.StdEncoding.EncodeToString(message), MessageSHA256: hex.EncodeToString(digest[:]),
		RequiredSigners: []RequiredSigner{{Index: 0, Pubkey: req.FeePayer, Role: "fee_payer"}, {Index: 1, Pubkey: req.OwnerAuthority, Role: "owner_authority"}},
		RecentBlockhash: req.RecentBlockhash, LastValidBlockHeight: req.LastValidBlockHeight, TraderAccount: "trader",
		Manifest: DelegationManifest{Format: "solana_legacy_message", PhoenixProgramID: PhoenixProgramID, Action: req.Action, OwnerAuthority: req.OwnerAuthority, NewPositionAuthority: req.NewPositionAuthority, FeePayer: req.FeePayer, TraderAccount: "trader", AccountKeys: []AccountManifest{{}}, Instructions: []InstructionManifest{{ProgramID: PhoenixProgramID}}},
	}
}

func TestBuildDelegationValidatesOwnerAndRevoke(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "valid"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, action := range []DelegationAction{ActionDelegate, ActionRevoke} {
		req := fakeDelegationRequest()
		req.Action = action
		if action == ActionRevoke {
			req.NewPositionAuthority = req.OwnerAuthority
		}
		result, err := client.BuildDelegation(context.Background(), req)
		if err != nil || result.Manifest.Action != action || result.RequiredSigners[1].Pubkey != req.OwnerAuthority {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}

func TestBuildDelegationStopsHelperOnWrongSignerRole(t *testing.T) {
	client, err := startClient(context.Background(), fakeHelperCommand(t, "delegation_wrong_owner"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.BuildDelegation(context.Background(), fakeDelegationRequest()); err == nil {
		t.Fatal("agent role accepted for owner delegation")
	}
	if _, err := client.Health(context.Background()); err == nil {
		t.Fatal("invalid helper remained usable")
	}
}

func TestDelegationResultRejectsMismatchedIntentAndEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*BuildDelegationRequest, *BuildDelegationResult)
	}{
		{"digest", func(_ *BuildDelegationRequest, r *BuildDelegationResult) { r.MessageSHA256 = "wrong" }},
		{"blockhash", func(_ *BuildDelegationRequest, r *BuildDelegationResult) { r.RecentBlockhash = "different" }},
		{"expiry", func(_ *BuildDelegationRequest, r *BuildDelegationResult) { r.LastValidBlockHeight++ }},
		{"isolated", func(_ *BuildDelegationRequest, r *BuildDelegationResult) { r.SubaccountIndex = 1 }},
		{"owner", func(_ *BuildDelegationRequest, r *BuildDelegationResult) { r.Manifest.OwnerAuthority = "different" }},
		{"target", func(_ *BuildDelegationRequest, r *BuildDelegationResult) {
			r.Manifest.NewPositionAuthority = "different"
		}},
		{"program", func(_ *BuildDelegationRequest, r *BuildDelegationResult) {
			r.Manifest.Instructions[0].ProgramID = FlightProgramID
		}},
		{"extra instruction", func(_ *BuildDelegationRequest, r *BuildDelegationResult) {
			r.Manifest.Instructions = append(r.Manifest.Instructions, InstructionManifest{})
		}},
		{"signer index", func(_ *BuildDelegationRequest, r *BuildDelegationResult) { r.RequiredSigners[1].Index = 0 }},
		{"invalid action", func(q *BuildDelegationRequest, _ *BuildDelegationResult) { q.Action = "open" }},
		{"revoke other delegate", func(q *BuildDelegationRequest, _ *BuildDelegationResult) { q.Action = ActionRevoke }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := fakeDelegationRequest()
			result := fakeDelegationResult(req)
			tc.change(&req, &result)
			if err := validateBuildDelegationResult(req, result); err == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}
