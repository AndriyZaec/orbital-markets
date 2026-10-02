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

func TestSigningStoreBlocksCloseReprepareAfterSubmissionStarts(t *testing.T) {
	store := NewSigningRequestStore()
	expires := time.Now().Add(time.Minute)
	requests := []*SigningRequest{
		{ID: "close-1", ClientOrderID: "order-1", PositionID: "position-1", Leg: 1, Action: "close", Venue: "pacifica", Account: "owner", Signer: "agent", ExpiresAt: expires},
		{ID: "close-2", ClientOrderID: "order-2", PositionID: "position-1", Leg: 2, Action: "close", Venue: "pacifica", Account: "owner", Signer: "agent", ExpiresAt: expires},
	}
	stored, reused, inProgress := store.ReuseOrStoreCloseBatch("position-1", requests)
	if len(stored) != 2 || reused || inProgress {
		t.Fatalf("initial batch = (%d, %t, %t), want (2, false, false)", len(stored), reused, inProgress)
	}
	if _, err := store.ValidateAndConsume(SignedAction{
		RequestID: "close-1", ClientOrderID: "order-1", Venue: "pacifica", SignerAddress: "agent", Signature: "signature",
	}); err != nil {
		t.Fatal(err)
	}

	replacement := []*SigningRequest{{
		ID: "replacement", PositionID: "position-1", Action: "close", ExpiresAt: expires,
	}}
	stored, reused, inProgress = store.ReuseOrStoreCloseBatch("position-1", replacement)
	if len(stored) != 0 || reused || !inProgress {
		t.Fatalf("started batch = (%d, %t, %t), want (0, false, true)", len(stored), reused, inProgress)
	}

	store.CompleteCloseSubmission(requests[0])
	stored, reused, inProgress = store.ReuseOrStoreCloseBatch("position-1", replacement)
	if len(stored) != 1 || stored[0].ID != "close-2" || !reused || inProgress {
		t.Fatal("completed close outcome did not release the remaining prepared leg")
	}
	if _, err := store.ValidateAndConsume(SignedAction{
		RequestID: "close-2", ClientOrderID: "order-2", Venue: "pacifica", SignerAddress: "agent", Signature: "signature",
	}); err != nil {
		t.Fatal(err)
	}
	store.CompleteCloseSubmission(requests[1])
	stored, reused, inProgress = store.ReuseOrStoreCloseBatch("position-1", replacement)
	if len(stored) != 1 || reused || inProgress {
		t.Fatal("fully rejected batch did not allow a fresh close prepare")
	}
}

func TestSigningStoreReusesCompleteBatchAfterSubmissionWasNotSent(t *testing.T) {
	store := NewSigningRequestStore()
	expires := time.Now().Add(time.Minute)
	requests := []*SigningRequest{
		{ID: "close-1", ClientOrderID: "order-1", PositionID: "position-1", Leg: 1, Action: "close", Venue: "pacifica", Account: "owner", Signer: "agent", ExpiresAt: expires},
		{ID: "close-2", ClientOrderID: "order-2", PositionID: "position-1", Leg: 2, Action: "close", Venue: "pacifica", Account: "owner", Signer: "agent", ExpiresAt: expires},
	}
	store.ReuseOrStoreCloseBatch("position-1", requests)
	consumed, err := store.ValidateAndConsume(SignedAction{
		RequestID: "close-1", ClientOrderID: "order-1", Venue: "pacifica", SignerAddress: "agent", Signature: "signature",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Store(consumed)

	existing, found, inProgress := store.ExistingCloseBatch("position-1")
	if !found || inProgress || len(existing) != 2 {
		t.Fatalf("restored batch = (%d, %t, %t), want (2, true, false)", len(existing), found, inProgress)
	}
}
