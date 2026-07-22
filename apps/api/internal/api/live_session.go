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
	// sessLeg1Submitted: leg 1 was accepted; fill state must be reconciled after a restart.
	sessLeg1Submitted sessionState = "leg1_submitted"
	// sessLeg1Submitting is persisted before the venue call because acceptance
	// can happen before the HTTP response reaches this process.
	sessLeg1Submitting sessionState = "leg1_submitting"
	// sessLeg2Submitted: the hedge was accepted; both venue positions must be reconciled.
	sessLeg2Submitted sessionState = "leg2_submitted"
	// sessLeg2Submitting conservatively means either or both legs may exist.
	sessLeg2Submitting sessionState = "leg2_submitting"
	// sessRecovering: startup/expiry recovery owns the session and will unwind or degrade it.
	sessRecovering sessionState = "recovering"
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

// LiveSession holds orchestration state for one non-custodial two-leg open.
// Active sessions are journaled so possible exposure can be reconciled after
// process restart.
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
	Leg1OpenReq     *domain.SigningRequest
	Leg1UnwindReq   *domain.SigningRequest
	Leg2OpenReq     *domain.SigningRequest

	// Armed reduce-only unwind for leg 1 — signed up front, held to fire on any
	// failure after leg 1 opens. Reduce-only auto-caps to the actual open size.
	ArmedUnwindSigned *domain.SignedAction
	ArmedUnwindReq    *domain.SigningRequest

	// Fills captured as the flow progresses.
	Leg1Fill *normFill
	Leg2Fill *normFill

	// Signed venue position size before leg 1 submission. Recovery compares
	// current venue truth with this baseline to isolate this session's exposure.
	BaselineLeg1Size float64
	BaselineLeg2Size float64

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *LiveSession) expired() bool {
	return time.Since(s.CreatedAt) > sessionTTL
}

func (s *LiveSession) hasPossibleExposure() bool {
	return s.State == sessLeg1Submitting || s.State == sessLeg1Submitted || s.State == sessAwaitingLeg2Sign ||
		s.State == sessLeg2Submitting || s.State == sessLeg2Submitted || s.State == sessRecovering
}

// SessionManager is a thread-safe in-memory store of live open sessions.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*LiveSession
	inFlight map[string]bool
}

func NewSessionManager() *SessionManager {
	return &SessionManager{sessions: make(map[string]*LiveSession), inFlight: make(map[string]bool)}
}

func (m *SessionManager) put(s *LiveSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.UpdatedAt = time.Now()
	m.sessions[s.ID] = s
}

func (m *SessionManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	delete(m.inFlight, id)
}

func (m *SessionManager) claim(id string) (*LiveSession, bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, found := m.sessions[id]
	if !found {
		return nil, false, false
	}
	if m.inFlight[id] {
		return nil, true, false
	}
	m.inFlight[id] = true
	return session, true, true
}

func (m *SessionManager) release(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.inFlight, id)
}

func (m *SessionManager) contains(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[id]
	return ok
}

func (m *SessionManager) activeSessionIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

// takeExpired removes and returns expired sessions for durable recovery.
func (m *SessionManager) takeExpired() []*LiveSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	var expired []*LiveSession
	for id, s := range m.sessions {
		if s.expired() && !m.inFlight[id] {
			delete(m.sessions, id)
			expired = append(expired, s)
		}
	}
	return expired
}
