package live

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mr-tron/base58"
)

func TestAgentRevokerValidatesAndRelaysPacificaWireShape(t *testing.T) {
	request := validRevokeAgentRequest(t, time.Now().UnixMilli())
	var received RevokeAgentRequest
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer venue.Close()

	if err := request.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	revoker := NewAgentRevoker(venue.URL, venue.Client())
	if err := revoker.RevokeAgent(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if received != request {
		t.Fatalf("received = %+v, want %+v", received, request)
	}
}

func TestAgentRevokerRejectsWrongOwnerSignature(t *testing.T) {
	request := validRevokeAgentRequest(t, time.Now().UnixMilli())
	request.Signature = base58.Encode(make([]byte, ed25519.SignatureSize))
	if err := request.Validate(time.Now()); err == nil {
		t.Fatal("invalid signature was accepted")
	}
}

func TestAgentRevokerPropagatesPacificaRejection(t *testing.T) {
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"agent not found"}`))
	}))
	defer venue.Close()

	revoker := NewAgentRevoker(venue.URL, venue.Client())
	err := revoker.RevokeAgent(context.Background(), validRevokeAgentRequest(t, time.Now().UnixMilli()))
	if err == nil || err.Error() != "Pacifica rejected agent revocation: agent not found" {
		t.Fatalf("error = %v", err)
	}
}

func validRevokeAgentRequest(t *testing.T, timestamp int64) RevokeAgentRequest {
	t.Helper()
	owner := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	agentSeed := make([]byte, ed25519.SeedSize)
	for index := range agentSeed {
		agentSeed[index] = byte(index + 32)
	}
	agent := ed25519.NewKeyFromSeed(agentSeed)
	agentAddress := base58.Encode(agent.Public().(ed25519.PublicKey))
	message, err := BuildSigningMessage("revoke_agent_wallet", timestamp, revokeExpiryWindow, map[string]any{
		"agent_wallet": agentAddress,
	})
	if err != nil {
		t.Fatal(err)
	}
	return RevokeAgentRequest{
		Account:   base58.Encode(owner.Public().(ed25519.PublicKey)),
		Signature: base58.Encode(ed25519.Sign(owner, message)), Timestamp: timestamp,
		ExpiryWindow: revokeExpiryWindow, AgentWallet: agentAddress,
	}
}
