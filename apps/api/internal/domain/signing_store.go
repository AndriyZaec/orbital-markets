package domain

import (
	"fmt"
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
	s.requests[req.ID] = req
}

// ValidateAndConsume atomically validates a signed action against a stored
// signing request and removes it from the store. This prevents double-submit:
// a second request with the same ID will fail with "unknown request id".
func (s *SigningRequestStore) ValidateAndConsume(signed SignedAction) (*SigningRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	// Consume atomically — no second submit possible
	delete(s.requests, signed.RequestID)

	return req, nil
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
