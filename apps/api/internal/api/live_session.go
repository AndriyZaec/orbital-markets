package api

import (
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

// sessionState tracks where a live open session is in the two-phase signing flow.
type sessionState string

const (
	// sessAwaitingLeg1Signs: prepare done; waiting for signed leg-1 open + leg-1 unwind.
	sessAwaitingLeg1Signs sessionState = "awaiting_leg1_signs"
	// sessAwaitingLeg2Sign: leg 1 filled; waiting for signed leg-2 open (sized from actual fill).
	sessAwaitingLeg2Sign sessionState = "awaiting_leg2_sign"
	// terminal states
	sessOpen     sessionState = "open"
	sessDegraded sessionState = "degraded"
	sessAborted  sessionState = "aborted"
	sessFailed   sessionState = "failed"
)

// sessionTTL bounds how long a session can sit between steps before it is
// considered stale. Kept short — the whole open flow is meant to be seconds.
const sessionTTL = 3 * time.Minute

// legPlan captures the resolved per-leg parameters after riskier-first ordering.
type legPlan struct {
	venue  string
	symbol string
	side   domain.Side
	price  float64
}

// normFill is a venue-agnostic fill summary used inside the session orchestrator.
type normFill struct {
	FilledAmount float64
	AvgFillPrice float64
	Fee          float64
	OrderID      string
	Status       string
	Filled       bool // filled or partial with amount > 0
}

// LiveSession holds the transient orchestration state for one non-custodial
// two-leg open. It is in-memory only: a server restart drops it, which is an
// accepted first-live risk (kill switch is the backstop).
type LiveSession struct {
	ID   string
	Plan *domain.ExecutionPlan

	// Resolved riskier-first ordering. Leg1 is submitted first.
	Leg1 legPlan // riskier leg
	Leg2 legPlan // hedge leg

	AccountPacifica    string
	AccountHyperliquid string

	State sessionState

	// Signing request correlation IDs issued to the frontend.
	Leg1OpenReqID   string
	Leg1UnwindReqID string
	Leg2OpenReqID   string

	// Armed reduce-only unwind for leg 1 — signed up front, held to fire on any
	// failure after leg 1 opens. Reduce-only auto-caps to the actual open size.
	ArmedUnwindSigned *domain.SignedAction
	ArmedUnwindReq    *domain.SigningRequest

	// Fills captured as the flow progresses.
	Leg1Fill *normFill
	Leg2Fill *normFill

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *LiveSession) expired() bool {
	return time.Since(s.CreatedAt) > sessionTTL
}

// SessionManager is a thread-safe in-memory store of live open sessions.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*LiveSession
}

func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: make(map[string]*LiveSession)}
}

func (m *SessionManager) put(s *LiveSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.UpdatedAt = time.Now()
	m.sessions[s.ID] = s
}

// get returns a live session by ID. The second return is false if missing or expired
// (expired sessions are evicted on access).
func (m *SessionManager) get(id string) (*LiveSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	if s.expired() {
		delete(m.sessions, id)
		return nil, false
	}
	return s, true
}

func (m *SessionManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// cleanup evicts expired sessions. Safe to call periodically.
func (m *SessionManager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if s.expired() {
			delete(m.sessions, id)
		}
	}
}
