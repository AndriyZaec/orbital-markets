package domain

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// SigningRequestStore holds pending signing requests in memory.
// Requests are stored when the backend builds them and consumed
// when the frontend returns a signed action.
type SigningRequestStore struct {
	mu       sync.Mutex
	requests map[string]*SigningRequest // keyed by request ID
}

func NewSigningRequestStore() *SigningRequestStore {
	return &SigningRequestStore{
		requests: make(map[string]*SigningRequest),
	}
}

// Store saves a signing request for later retrieval.
func (s *SigningRequestStore) Store(req *SigningRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, pending := range s.requests {
		if now.After(pending.ExpiresAt) {
			delete(s.requests, id)
		}
	}
	if now.After(req.ExpiresAt) {
		return
	}
	s.requests[req.ID] = req
}

// Validate checks a signed action without consuming it. Callers use this to
// perform authorization and operation checks before the one-shot consume.
func (s *SigningRequestStore) Validate(signed SignedAction) (*SigningRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateLocked(signed, false)
}

// ValidateAndConsume atomically validates a signed action against a stored
// signing request and removes it from the store. This prevents double-submit:
// a second request with the same ID will fail with "unknown request id".
func (s *SigningRequestStore) ValidateAndConsume(signed SignedAction) (*SigningRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateLocked(signed, true)
}

func (s *SigningRequestStore) validateLocked(signed SignedAction, consume bool) (*SigningRequest, error) {
	req, exists := s.requests[signed.RequestID]
	if !exists {
		return nil, fmt.Errorf("unknown request id: %s", signed.RequestID)
	}

	if req.ClientOrderID != signed.ClientOrderID {
		return nil, fmt.Errorf(
			"client_order_id mismatch: expected %s, got %s",
			req.ClientOrderID, signed.ClientOrderID,
		)
	}

	if req.Venue != signed.Venue {
		return nil, fmt.Errorf(
			"venue mismatch: expected %s, got %s",
			req.Venue, signed.Venue,
		)
	}
	expectedSigner := req.Signer
	if expectedSigner == "" {
		expectedSigner = req.Account
	}
	if expectedSigner != "" && !signingAccountMatches(req.Venue, expectedSigner, signed.SignerAddress) {
		return nil, fmt.Errorf("signer does not match prepared %s signer", req.Venue)
	}

	if time.Now().After(req.ExpiresAt) {
		delete(s.requests, signed.RequestID)
		return nil, fmt.Errorf(
			"request expired at %s (%.1fs ago)",
			req.ExpiresAt.Format(time.RFC3339),
			time.Since(req.ExpiresAt).Seconds(),
		)
	}

	if signed.Signature == "" {
		return nil, fmt.Errorf("empty signature")
	}

	if signed.SignerAddress == "" {
		return nil, fmt.Errorf("empty signer address")
	}

	if consume {
		delete(s.requests, signed.RequestID)
	}

	return req, nil
}

func signingAccountMatches(venue, expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if venue == "hyperliquid" || venue == "aster" {
		return strings.EqualFold(expected, actual)
	}
	return expected == actual
}

// Cleanup removes expired requests older than maxAge beyond their expiry.
func (s *SigningRequestStore) Cleanup(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for id, req := range s.requests {
		if req.ExpiresAt.Before(cutoff) {
			delete(s.requests, id)
		}
	}
}
