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

func TestAgentBinderRelaysPacificaWireShape(t *testing.T) {
	request := validBindAgentRequest(t, time.Now().UnixMilli())
	var received BindAgentRequest
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer venue.Close()

	binder := NewAgentBinder(venue.URL, venue.Client())
	if err := binder.BindAgent(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if received != request {
		t.Fatalf("received = %+v, want %+v", received, request)
	}
}

func TestAgentBinderPropagatesPacificaRejection(t *testing.T) {
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid signature"}`))
	}))
	defer venue.Close()

	binder := NewAgentBinder(venue.URL, venue.Client())
	err := binder.BindAgent(context.Background(), validBindAgentRequest(t, time.Now().UnixMilli()))
	if err == nil || err.Error() != "Pacifica rejected agent binding: invalid signature" {
		t.Fatalf("error = %v", err)
	}
}

func validBindAgentRequest(t *testing.T, timestamp int64) BindAgentRequest {
	t.Helper()
	owner := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	agentSeed := make([]byte, ed25519.SeedSize)
	for index := range agentSeed {
		agentSeed[index] = byte(index + 32)
	}
	agent := ed25519.NewKeyFromSeed(agentSeed)
	agentAddress := base58.Encode(agent.Public().(ed25519.PublicKey))
	message, err := BuildSigningMessage("bind_agent_wallet", timestamp, 5_000, map[string]any{
		"agent_wallet": agentAddress,
	})
	if err != nil {
		t.Fatal(err)
	}
	return BindAgentRequest{
		Account:   base58.Encode(owner.Public().(ed25519.PublicKey)),
		Signature: base58.Encode(ed25519.Sign(owner, message)),
		Timestamp: timestamp, ExpiryWindow: 5_000, AgentWallet: agentAddress,
	}
}
