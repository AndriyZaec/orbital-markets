package domain

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// SigningRequestStore holds pending signing requests in memory.
// Requests are stored when the backend builds them and consumed
// when the frontend returns a signed action.
type SigningRequestStore struct {
	mu           sync.Mutex
	requests     map[string]*SigningRequest // keyed by request ID
	closeBatches map[string]*closeSigningBatch
}

type closeSigningBatch struct {
	requests []*SigningRequest
	expires  time.Time
	inFlight map[string]struct{}
	complete map[string]struct{}
}

// ExistingCloseBatch returns an existing prepared batch without requiring the
// caller to rebuild venue payloads first.
func (s *SigningRequestStore) ExistingCloseBatch(positionID string) ([]*SigningRequest, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupCloseBatchesLocked(time.Now())
	return s.existingCloseBatchLocked(positionID)
}

// ReuseOrStoreCloseBatch atomically keeps one active prepared request set per
// position. Repeated preparation returns the still-valid set instead of
// creating independently signable duplicate close orders.
func (s *SigningRequestStore) ReuseOrStoreCloseBatch(positionID string, requests []*SigningRequest) ([]*SigningRequest, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.cleanupCloseBatchesLocked(now)
	if existing, found, inProgress := s.existingCloseBatchLocked(positionID); found {
		return existing, !inProgress, inProgress
	}
	expires := time.Time{}
	for _, request := range requests {
		if !now.After(request.ExpiresAt) {
			s.requests[request.ID] = request
			if expires.IsZero() || request.ExpiresAt.Before(expires) {
				expires = request.ExpiresAt
			}
		}
	}
	if !expires.IsZero() {
		s.closeBatches[positionID] = &closeSigningBatch{
			requests: append([]*SigningRequest(nil), requests...),
			expires:  expires,
			inFlight: make(map[string]struct{}),
			complete: make(map[string]struct{}),
		}
	}
	return requests, false, false
}

func NewSigningRequestStore() *SigningRequestStore {
	return &SigningRequestStore{
		requests:     make(map[string]*SigningRequest),
		closeBatches: make(map[string]*closeSigningBatch),
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
	if req.PositionID != "" && req.Action == "close" {
		if batch := s.closeBatches[req.PositionID]; batch != nil {
			delete(batch.inFlight, req.ID)
		}
	}
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
		if req.PositionID != "" && req.Action == "close" {
			if batch := s.closeBatches[req.PositionID]; batch != nil {
				batch.inFlight[req.ID] = struct{}{}
			}
		}
		delete(s.requests, signed.RequestID)
	}

	return req, nil
}

func (s *SigningRequestStore) CompleteCloseSubmission(req *SigningRequest) {
	if req == nil || req.PositionID == "" || req.Action != "close" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := s.closeBatches[req.PositionID]
	if batch == nil {
		return
	}
	delete(batch.inFlight, req.ID)
	batch.complete[req.ID] = struct{}{}
	if len(batch.complete) == len(batch.requests) {
		delete(s.closeBatches, req.PositionID)
	}
}

func (s *SigningRequestStore) existingCloseBatchLocked(positionID string) ([]*SigningRequest, bool, bool) {
	batch := s.closeBatches[positionID]
	if batch == nil {
		return nil, false, false
	}
	if len(batch.inFlight) > 0 {
		return nil, true, true
	}
	result := make([]*SigningRequest, 0, len(batch.requests)-len(batch.complete))
	for _, request := range batch.requests {
		if _, complete := batch.complete[request.ID]; complete {
			continue
		}
		if _, pending := s.requests[request.ID]; pending {
			result = append(result, request)
		}
	}
	if len(result) == 0 {
		return nil, true, true
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Leg < result[j].Leg })
	return result, true, false
}

func (s *SigningRequestStore) cleanupCloseBatchesLocked(now time.Time) {
	for id, pending := range s.requests {
		if now.After(pending.ExpiresAt) {
			delete(s.requests, id)
		}
	}
	for id, batch := range s.closeBatches {
		if now.After(batch.expires) {
			for _, request := range batch.requests {
				delete(s.requests, request.ID)
			}
			delete(s.closeBatches, id)
		}
	}
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
