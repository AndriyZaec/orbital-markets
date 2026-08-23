package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const recoveryAccountTimeout = 20 * time.Second

func (s *Server) runLiveSessionRecovery() {
	go s.renewLiveSessionLeases()
	s.restoreLiveSessions()
	s.reconcileClosingPositions()
	s.reconcileOpenPositions()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.restoreLiveSessions()
			s.cleanupExpiredLiveSessions()
			s.reconcileClosingPositions()
			s.reconcileOpenPositions()
		}
	}
}

const cachedExposureAmountTolerance = 0.005

func (s *Server) reconcileOpenPositions() {
	if s.live == nil || s.live.accounts == nil {
		return
	}
	positions, err := s.liveStore.ListOpenPositions(s.ctx)
	if err != nil {
		s.logger.Error("live exposure recovery: list positions", "err", err)
		return
	}
	for i := range positions {
		position := &positions[i]
		exposure, ready, err := s.cachedVenueExposure(position)
		if err != nil {
			s.logger.Warn("live exposure recovery: account snapshots unavailable", "err", err, "id", position.ID)
			continue
		}
		if !ready {
			continue
		}
		if math.Abs(exposure["pacifica"]) <= 1e-9 && math.Abs(exposure["hyperliquid"]) <= 1e-9 {
			if _, err := s.markPositionClosedFromVenueTruth(s.ctx, position); err != nil {
				s.logger.Warn("live exposure recovery: mark flat position closed", "err", err, "id", position.ID)
			}
			continue
		}
		if position.State != string(executor.ExecStateOpen) {
			continue
		}
		fills, err := s.liveStore.GetFills(s.ctx, position.ID)
		if err != nil {
			s.logger.Warn("live exposure recovery: get fills", "err", err, "id", position.ID)
			continue
		}
		if cachedExposureMatchesFills(exposure, fills) {
			continue
		}
		if err := s.markCloseDegraded(s.ctx, position.ID); err == nil {
			s.liveStore.InsertEvent(s.ctx, position.ID, "venue_exposure_changed", executor.ExecStateDegraded,
				"fresh account snapshots no longer match the recorded open legs")
		}
	}
}

func (s *Server) cachedVenueExposure(position *executor.LivePosition) (map[string]float64, bool, error) {
	accounts, err := s.live.acquireRecoveryAccounts(position.AccountPacifica, position.AccountHyperliquid)
	if err != nil {
		return nil, false, err
	}
	defer accounts.Release()

	after, err := time.Parse(time.RFC3339, position.OpenedAt)
	if err != nil {
		after, err = time.Parse(time.RFC3339, position.UpdatedAt)
	}
	if err != nil {
		return nil, false, nil
	}
	exposure := make(map[string]float64, 2)
	for _, venue := range []string{"pacifica", "hyperliquid"} {
		feed, ok := accounts.Feed(venue)
		if !ok {
			return nil, false, nil
		}
		snapshot := feed.Snapshot()
		if snapshot.PositionsUpdatedAt.IsZero() || !snapshot.PositionsUpdatedAt.After(after) ||
			time.Since(snapshot.PositionsUpdatedAt) > admissionFreshness {
			return nil, false, nil
		}
		for _, accountPosition := range snapshot.Positions {
			if strings.EqualFold(accountPosition.Symbol, position.Asset) {
				exposure[venue] = signedSize(accountPosition.Side, accountPosition.Size)
				break
			}
		}
	}
	return exposure, true, nil
}

func cachedExposureMatchesFills(exposure map[string]float64, fills []executor.LiveFill) bool {
	expected := make(map[string]float64, 2)
	for _, fill := range fills {
		if fill.Filled && fill.FilledAmount > 0 {
			expected[fill.Venue] = signedSize(fill.Side, fill.FilledAmount)
		}
	}
	if len(expected) != 2 {
		return false
	}
	for _, venue := range []string{"pacifica", "hyperliquid"} {
		expectedSize, ok := expected[venue]
		if !ok {
			return false
		}
		tolerance := math.Max(math.Abs(expectedSize)*cachedExposureAmountTolerance, 1e-9)
		if math.Abs(exposure[venue]-expectedSize) > tolerance {
			return false
		}
	}
	return true
}

func (s *Server) reconcileClosingPositions() {
	if s.live == nil || s.live.accounts == nil {
		return
	}
	positions, err := s.liveStore.ListClosingPositions(s.ctx)
	if err != nil {
		s.logger.Error("live close recovery: list closing positions", "err", err)
		return
	}
	for i := range positions {
		position := &positions[i]
		ctx, cancel := context.WithTimeout(s.ctx, recoveryAccountTimeout)
		exposureState, err := s.inspectPositionAfterCloseActivity(
			ctx, position, position.AccountPacifica, position.AccountHyperliquid,
		)
		cancel()
		if err != nil {
			s.logger.Warn("live close recovery: venue reconciliation failed", "err", err, "id", position.ID)
			continue
		}
		switch exposureState {
		case positionExposureFlat:
			if _, err := s.markPositionClosedFromVenueTruth(s.ctx, position); err != nil {
				s.logger.Warn("live close recovery: mark closed", "err", err, "id", position.ID)
			}
		case positionExposurePresent:
			progress, progressErr := s.liveStore.GetCloseProgress(s.ctx, position.ID)
			if progressErr != nil {
				s.logger.Warn("live close recovery: get close progress", "err", progressErr, "id", position.ID)
				continue
			}
			if progress.Required > 0 && progress.Confirmed == progress.Required && progress.Pending == 0 {
				if err := s.markCloseDegraded(s.ctx, position.ID); err == nil {
					s.liveStore.InsertEvent(s.ctx, position.ID, "close_residual", executor.ExecStateDegraded,
						"confirmed close fills left residual venue exposure")
				}
			}
		}
	}
}

func (s *Server) renewLiveSessionLeases() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			for _, sessionID := range s.live.sessions.activeSessionIDs() {
				claimed, err := s.liveStore.ClaimDurableSession(s.ctx, sessionID, s.recoveryOwner, sessionRecoveryLease)
				if err != nil || !claimed {
					s.logger.Error("live recovery: renew session lease", "err", err, "session_id", sessionID)
				}
			}
		}
	}
}

func (s *Server) restoreLiveSessions() {
	records, err := s.liveStore.ListActiveDurableSessions(s.ctx)
	if err != nil {
		s.logger.Error("live recovery: load sessions", "err", err)
		return
	}
	for _, record := range records {
		if s.live.sessions.contains(record.ID) {
			continue
		}
		if record.HasExposure {
			claimed, err := s.liveStore.ClaimDurableSession(s.ctx, record.ID, s.recoveryOwner, sessionRecoveryLease)
			if err != nil {
				s.logger.Error("live recovery: claim session record", "err", err, "session_id", record.ID)
				continue
			}
			if !claimed {
				continue
			}
		}
		if record.DecodeError != "" {
			s.logger.Error("live recovery: decode session envelope", "err", record.DecodeError, "session_id", record.ID)
			if record.HasExposure {
				detail := "invalid exposed session envelope: " + record.DecodeError
				_ = s.liveStore.FlagDurableSession(s.ctx, record.ID, "recovery_blocked", detail)
				_ = s.liveStore.UpsertRecoveryBlockedPosition(
					s.ctx, record.ID, record.Asset,
					record.AccountPacifica, record.AccountHyperliquid, detail,
				)
			} else {
				s.finishSafeDurableSession(record.ID, "recovery_invalid_safe", record.DecodeError)
			}
			continue
		}
		session, err := unmarshalLiveSession(record.Payload)
		if err != nil {
			s.logger.Error("live recovery: decode session", "err", err, "session_id", record.ID)
			if record.HasExposure {
				detail := "invalid exposed session payload: " + err.Error()
				_ = s.liveStore.FlagDurableSession(s.ctx, record.ID, "recovery_blocked", detail)
				_ = s.liveStore.UpsertRecoveryBlockedPosition(
					s.ctx, record.ID, record.Asset,
					record.AccountPacifica, record.AccountHyperliquid, detail,
				)
			} else {
				s.finishSafeDurableSession(record.ID, "recovery_invalid_safe", err.Error())
			}
			continue
		}
		if _, err := s.liveStore.GetPosition(s.ctx, session.Plan.ID); err == nil {
			claimed, claimErr := s.liveStore.ClaimDurableSession(s.ctx, session.ID, s.recoveryOwner, sessionRecoveryLease)
			if claimErr == nil && claimed {
				_ = s.liveStore.FinishDurableSessionOwned(
					s.ctx, session.ID, s.recoveryOwner, "already_persisted", "position already persisted")
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			s.logger.Error("live recovery: check position", "err", err, "session_id", session.ID)
			continue
		}

		if session.State == sessConfiguringLeverage {
			session.State = sessFailed
			detail := "server restarted while applying leverage; venue leverage may be partially changed, but no order was submitted. Retry to reconcile both venues"
			s.finishLiveSession(s.ctx, session, detail)
			continue
		}
		if session.State == sessAwaitingLeg1Signs {
			if !agentBoundLeg1Requests(session) {
				session.State = sessFailed
				s.finishSafeDurableSession(session.ID, "legacy_safe", "pre-agent session cannot start new signing work")
				continue
			}
			if session.expired() || session.Leg1OpenReq == nil || session.Leg1UnwindReq == nil ||
				session.PacificaLeverageReq == nil || session.HyperliquidLeverageReq == nil ||
				time.Now().After(session.Leg1OpenReq.ExpiresAt) || time.Now().After(session.Leg1UnwindReq.ExpiresAt) {
				session.State = sessFailed
				s.finishSafeDurableSession(session.ID, "expired_safe", "expired before any order submission")
				continue
			}
			s.live.signingStore.Store(session.Leg1OpenReq)
			s.live.signingStore.Store(session.Leg1UnwindReq)
			s.live.signingStore.Store(session.PacificaLeverageReq)
			s.live.signingStore.Store(session.HyperliquidLeverageReq)
			s.live.sessions.put(session)
			s.logger.Info("live recovery: restored pre-exposure session", "session_id", session.ID)
			continue
		}

		s.recoverExposedSession(session, "server restarted during live execution")
	}
}

func agentBoundLeg1Requests(session *LiveSession) bool {
	if session == nil || session.AgentPacifica == "" || session.AgentHyperliquid == "" ||
		session.Leg1OpenReq == nil || session.Leg1UnwindReq == nil ||
		session.PacificaLeverageReq == nil || session.HyperliquidLeverageReq == nil {
		return false
	}
	for _, request := range []*domain.SigningRequest{
		session.Leg1OpenReq, session.Leg1UnwindReq,
		session.PacificaLeverageReq, session.HyperliquidLeverageReq,
	} {
		expected := signerForVenue(request.Venue, session.AgentPacifica, session.AgentHyperliquid)
		if expected == "" || request.Signer == "" {
			return false
		}
		if request.Venue == "hyperliquid" {
			if !strings.EqualFold(expected, request.Signer) {
				return false
			}
		} else if expected != request.Signer {
			return false
		}
	}
	return true
}

func (s *Server) cleanupExpiredLiveSessions() {
	for _, session := range s.live.sessions.takeExpired() {
		if !session.hasPossibleExposure() {
			session.State = sessFailed
			s.finishSafeDurableSession(session.ID, "expired_safe", "expired before any order submission")
			continue
		}
		go s.recoverExposedSession(session, "live session expired with possible exposure")
	}
}

func (s *Server) recoverExpiredLiveSession(session *LiveSession) {
	if !session.hasPossibleExposure() {
		session.State = sessFailed
		s.finishSafeDurableSession(session.ID, "expired_safe", "expired before any order submission")
		return
	}
	go s.recoverExposedSession(session, "live session expired with possible exposure")
}

func (s *Server) finishSafeDurableSession(id, state, detail string) {
	claimed, err := s.liveStore.ClaimDurableSession(s.ctx, id, s.recoveryOwner, sessionRecoveryLease)
	if err != nil || !claimed {
		return
	}
	_ = s.liveStore.FinishDurableSessionOwned(s.ctx, id, s.recoveryOwner, state, detail)
}

func (s *Server) recoverExposedSession(session *LiveSession, reason string) {
	claimedLease, err := s.liveStore.ClaimDurableSession(s.ctx, session.ID, s.recoveryOwner, sessionRecoveryLease)
	if err != nil {
		s.logger.Error("live recovery: claim durable session", "err", err, "session_id", session.ID)
		return
	}
	if !claimedLease {
		s.logger.Info("live recovery: session owned by another server", "session_id", session.ID)
		return
	}
	accounts, err := s.live.acquireRecoveryAccounts(session.AccountPacifica, session.AccountHyperliquid)
	if err != nil {
		s.logger.Error("live recovery: account feeds unavailable", "err", err, "session_id", session.ID)
		return
	}
	defer accounts.Release()
	unlockAccounts := accounts.Lock()
	defer unlockAccounts()
	session.accounts = accounts
	defer func() { session.accounts = nil }()
	s.live.sessions.put(session)
	_, found, claimed := s.live.sessions.claim(session.ID)
	if !found || !claimed {
		return
	}
	defer s.live.sessions.release(session.ID)
	originalState := session.State
	ctx, cancel := context.WithTimeout(s.ctx, recoveryAccountTimeout)
	defer cancel()

	needLeg2 := originalState == sessAwaitingLeg2RetrySign ||
		originalState == sessLeg2Submitting || originalState == sessLeg2Submitted
	truthReady := s.waitForRecoveryAccountState(ctx, session, needLeg2)
	leg1Size, leg1Price := currentVenuePosition(accounts, session.Leg1.venue, session.Leg1.symbol)
	leg2Size, leg2Price := currentVenuePosition(accounts, session.Leg2.venue, session.Leg2.symbol)
	leg1Delta := leg1Size - session.BaselineLeg1Size
	leg2Delta := leg2Size - session.BaselineLeg2Size

	if !truthReady {
		leg1Amount := liveSessionLeg1Amount(session)
		leg2Amount := 0.0
		if needLeg2 {
			leg2Amount = liveSessionLeg2Amount(session)
		}
		s.ensureRecoveryFills(session, leg1Amount, leg2Amount, session.Leg1.price, session.Leg2.price)
		if !needLeg2 {
			unwindCtx, unwindCancel := context.WithTimeout(s.ctx, 20*time.Second)
			ur := s.fireUnwind(unwindCtx, session)
			unwindCancel()
			session.Leg1Fill = remainingFillAfterUnwind(session.Leg1Fill, ur.FilledAmount)
			sessionState, persistState := terminalStateAfterUnwind(ur)
			session.State = sessionState
			detail := reason + "; venue position state unavailable" + unwindReasonSuffix(ur)
			s.persistSession(s.ctx, session, persistState, detail)
			return
		}
		session.State = sessDegraded
		detail := reason + "; venue position state unavailable, manual action required"
		s.persistSession(s.ctx, session, executor.ExecStateDegraded, detail)
		return
	}

	leg1Exposed := exposureMatches(session.Leg1.side, leg1Delta)
	leg2Exposed := exposureMatches(session.Leg2.side, leg2Delta)
	if needLeg2 {
		s.reconcileSubmittedHedge(session, reason, leg1Delta, leg2Delta, leg1Price, leg2Price, leg1Exposed, leg2Exposed)
		return
	}
	if !leg1Exposed {
		session.Leg1Fill = nil
		session.Leg2Fill = nil
		session.State = sessFailed
		s.persistSession(s.ctx, session, executor.ExecStateFailed, reason+"; venue truth shows no leg-1 exposure")
		return
	}

	s.ensureRecoveryFills(session, math.Abs(leg1Delta), 0, leg1Price, 0)
	unwindCtx, unwindCancel := context.WithTimeout(s.ctx, 20*time.Second)
	ur := s.fireUnwind(unwindCtx, session)
	unwindCancel()
	session.Leg1Fill = remainingFillAfterUnwind(session.Leg1Fill, ur.FilledAmount)
	sessionState, persistState := terminalStateAfterUnwind(ur)
	session.State = sessionState
	detail := reason + "; leg-1 exposure reconciled from venue state" + unwindReasonSuffix(ur)
	s.persistSession(s.ctx, session, persistState, detail)
}

func (s *Server) reconcileSubmittedHedge(
	session *LiveSession,
	reason string,
	leg1Delta, leg2Delta, leg1Price, leg2Price float64,
	leg1Exposed, leg2Exposed bool,
) {
	s.ensureRecoveryFills(session, math.Abs(leg1Delta), math.Abs(leg2Delta), leg1Price, leg2Price)
	if !leg1Exposed && !leg2Exposed {
		session.State = sessFailed
		s.persistSession(s.ctx, session, executor.ExecStateFailed, reason+"; venue truth shows no remaining exposure")
		return
	}
	if leg1Exposed && leg2Exposed {
		mismatch := math.Abs(math.Abs(leg2Delta)-math.Abs(leg1Delta)) / math.Abs(leg1Delta)
		if mismatch <= maxHedgeMismatchPct {
			session.State = sessOpen
			s.persistSession(s.ctx, session, executor.ExecStateOpen, reason+"; both legs reconciled from venue state")
			return
		}
	}
	if leg1Exposed && !leg2Exposed {
		unwindCtx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
		ur := s.fireUnwind(unwindCtx, session)
		cancel()
		session.Leg1Fill = remainingFillAfterUnwind(session.Leg1Fill, ur.FilledAmount)
		sessionState, persistState := terminalStateAfterUnwind(ur)
		session.State = sessionState
		s.persistSession(s.ctx, session, persistState, reason+"; hedge absent"+unwindReasonSuffix(ur))
		return
	}
	session.State = sessDegraded
	s.persistSession(s.ctx, session, executor.ExecStateDegraded,
		reason+"; venue truth shows mismatched two-leg exposure, manual action required")
}

func (s *Server) waitForRecoveryAccountState(ctx context.Context, session *LiveSession, needLeg2 bool) bool {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		leg1Ready := venuePositionStateReadyAfter(session.accounts, session.Leg1.venue, session.UpdatedAt)
		leg2Ready := !needLeg2 || venuePositionStateReadyAfter(session.accounts, session.Leg2.venue, session.UpdatedAt)
		if leg1Ready && leg2Ready {
			// Position snapshots can arrive just after account equity readiness.
			select {
			case <-ctx.Done():
				return false
			case <-time.After(time.Second):
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func venuePositionStateReady(accounts *liveAccountContext, venue string) bool {
	return venuePositionStateReadyAfter(accounts, venue, time.Time{})
}

func venuePositionStateReadyAfter(accounts *liveAccountContext, venue string, after time.Time) bool {
	feed, ok := accounts.Feed(venue)
	if !ok {
		return false
	}
	return positionStateReady(feed.Snapshot().PositionsUpdatedAt, after)
}

func positionStateReady(updatedAt, after time.Time) bool {
	if updatedAt.IsZero() {
		return false
	}
	if after.IsZero() {
		return true
	}
	return updatedAt.After(after) && time.Since(updatedAt) <= admissionFreshness
}

func currentVenuePosition(accounts *liveAccountContext, venue, symbol string) (float64, float64) {
	feed, ok := accounts.Feed(venue)
	if !ok {
		return 0, 0
	}
	for _, position := range feed.Snapshot().Positions {
		if strings.EqualFold(position.Symbol, symbol) {
			return signedSize(position.Side, position.Size), position.EntryPrice
		}
	}
	return 0, 0
}

func signedSize(side string, size float64) float64 {
	if strings.EqualFold(side, "short") {
		return -math.Abs(size)
	}
	return math.Abs(size)
}

func exposureMatches(side domain.Side, delta float64) bool {
	const epsilon = 1e-9
	if side == domain.SideLong {
		return delta > epsilon
	}
	return delta < -epsilon
}

func (s *Server) ensureRecoveryFills(session *LiveSession, leg1Amount, leg2Amount, leg1Price, leg2Price float64) {
	if leg1Amount > 0 {
		session.Leg1Fill = &normFill{FilledAmount: leg1Amount, AvgFillPrice: leg1Price, Status: "reconciled", Filled: true}
	} else {
		session.Leg1Fill = nil
	}
	if leg2Amount > 0 {
		session.Leg2Fill = &normFill{FilledAmount: leg2Amount, AvgFillPrice: leg2Price, Status: "reconciled", Filled: true}
	} else {
		session.Leg2Fill = nil
	}
}

func recoveryPersistenceError(sessionID string, err error) string {
	return fmt.Sprintf("session %s persistence failed: %v", sessionID, err)
}
