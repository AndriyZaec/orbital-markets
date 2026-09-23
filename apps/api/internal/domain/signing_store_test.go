package domain

import (
	"testing"
	"time"
)

func TestSigningStoreRejectsWrongAccountWithoutConsumingRequest(t *testing.T) {
	store := NewSigningRequestStore()
	request := &SigningRequest{
		ID: "request-1", ClientOrderID: "order-1", Venue: "pacifica",
		Account: "SolCaseSensitive", ExpiresAt: time.Now().Add(time.Minute),
	}
	store.Store(request)

	signed := SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: request.Venue,
		SignerAddress: "wrong-wallet", Signature: "signature",
	}
	if _, err := store.ValidateAndConsume(signed); err == nil {
		t.Fatal("wrong signer account was accepted")
	}
	signed.SignerAddress = request.Account
	if _, err := store.ValidateAndConsume(signed); err != nil {
		t.Fatalf("request was consumed by failed account validation: %v", err)
	}
}

func TestSigningStoreMatchesHyperliquidAccountCaseInsensitively(t *testing.T) {
	store := NewSigningRequestStore()
	request := &SigningRequest{
		ID: "request-1", ClientOrderID: "order-1", Venue: "hyperliquid",
		Account: "0xOwner", Signer: "0xAbCd", ExpiresAt: time.Now().Add(time.Minute),
	}
	store.Store(request)
	_, err := store.ValidateAndConsume(SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: request.Venue,
		SignerAddress: "0xabcd", Signature: "signature",
	})
	if err != nil {
		t.Fatalf("case-insensitive Hyperliquid account rejected: %v", err)
	}
}

func TestSigningStoreSeparatesOwnerFromPacificaAgent(t *testing.T) {
	store := NewSigningRequestStore()
	request := &SigningRequest{
		ID: "request-1", ClientOrderID: "order-1", Venue: "pacifica",
		Account: "owner-wallet", Signer: "agent-wallet", ExpiresAt: time.Now().Add(time.Minute),
	}
	store.Store(request)
	signed := SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: request.Venue,
		SignerAddress: request.Account, Signature: "signature",
	}
	if _, err := store.ValidateAndConsume(signed); err == nil {
		t.Fatal("owner signature was accepted for an agent-bound request")
	}
	signed.SignerAddress = request.Signer
	if _, err := store.ValidateAndConsume(signed); err != nil {
		t.Fatalf("agent signature rejected: %v", err)
	}
}

func TestSigningStoreValidateDoesNotConsumeRequest(t *testing.T) {
	store := NewSigningRequestStore()
	request := &SigningRequest{
		ID: "request-1", ClientOrderID: "order-1", Venue: "aster",
		Account: "0xOwner", Signer: "0xAgent", ExpiresAt: time.Now().Add(time.Minute),
	}
	store.Store(request)
	signed := SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: request.Venue,
		SignerAddress: "0xagent", Signature: "signature",
	}

	if _, err := store.Validate(signed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateAndConsume(signed); err != nil {
		t.Fatalf("validation consumed request: %v", err)
	}
	if _, err := store.ValidateAndConsume(signed); err == nil {
		t.Fatal("consumed request was accepted twice")
	}
}

func TestSigningStoreDoesNotStoreExpiredRequest(t *testing.T) {
	store := NewSigningRequestStore()
	request := &SigningRequest{
		ID: "expired", Venue: "aster", Signer: "0xAgent", ExpiresAt: time.Now().Add(-time.Second),
	}
	store.Store(request)

	if _, err := store.Validate(SignedAction{
		RequestID: request.ID, Venue: request.Venue, SignerAddress: request.Signer, Signature: "signature",
	}); err == nil {
		t.Fatal("expired request was stored")
	}
}
