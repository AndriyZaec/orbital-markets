package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const leg2RetrySigningWindow = 5 * time.Second

func mergeNormFills(first, second *normFill) *normFill {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	total := first.FilledAmount + second.FilledAmount
	avgPrice := 0.0
	if total > 0 {
		avgPrice = (first.AvgFillPrice*first.FilledAmount + second.AvgFillPrice*second.FilledAmount) / total
	}
	return &normFill{
		FilledAmount: total,
		AvgFillPrice: avgPrice,
		Fee:          first.Fee + second.Fee,
		OrderID:      second.OrderID,
		Status:       second.Status,
		Filled:       total > 0,
	}
}

func hedgeMismatch(leg1Amount, leg2Amount float64) float64 {
	if leg1Amount <= 0 {
		return 1
	}
	return math.Abs(leg2Amount-leg1Amount) / leg1Amount
}

func remainingFillAfterUnwind(fill *normFill, unwoundAmount float64) *normFill {
	if fill == nil {
		return nil
	}
	remaining := math.Max(0, fill.FilledAmount-unwoundAmount)
	if remaining <= 1e-9 {
		return nil
	}
	copy := *fill
	copy.FilledAmount = remaining
	copy.Status = "remaining"
	copy.Filled = true
	return &copy
}

func (s *Server) prepareLeg2Retry(w http.ResponseWriter, ctx context.Context, session *LiveSession, reason string) {
	if session.Leg2Attempts >= 2 || session.Leg1Fill == nil {
		s.recoverInvalidHedge(w, ctx, session, reason)
		return
	}
	filled := 0.0
	if session.Leg2Fill != nil {
		filled = session.Leg2Fill.FilledAmount
	}
	remaining := session.Leg1Fill.FilledAmount - filled
	if remaining <= 0 {
		s.recoverInvalidHedge(w, ctx, session, reason)
		return
	}

	clientOrderID := fmt.Sprintf("orbital-l2retry-%d", time.Now().UnixNano())
	retryReq, err := s.buildOpenSigningRequest(
		session.Leg2, remaining, clientOrderID, session.AccountPacifica,
	)
	if err != nil {
		s.recoverInvalidHedge(w, ctx, session, reason+"; retry payload build failed")
		return
	}
	retryReq.ExpiresAt = time.Now().Add(leg2RetrySigningWindow)
	session.Leg2RetryReqID = retryReq.ID
	session.Leg2RetryReq = retryReq
	session.State = sessAwaitingLeg2RetrySign
	session.Recovery = append(session.Recovery, executor.RecoveryAction{
		Action: "retry_leg2",
		Detail: fmt.Sprintf("%s; residual=%.8f", reason, remaining),
	})
	s.live.sessions.put(session)
	if err := s.saveLiveSession(ctx, session); err != nil {
		s.recoverInvalidHedge(w, ctx, session, reason+"; retry persistence failed")
		return
	}
	s.live.signingStore.Store(retryReq)

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":       session.ID,
		"status":           string(sessAwaitingLeg2RetrySign),
		"leg1_fill":        fillView(session.Leg1Fill, 0),
		"leg2_fill":        fillView(session.Leg2Fill, 0),
		"mismatch":         hedgeMismatch(session.Leg1Fill.FilledAmount, filled),
		"reason":           reason,
		"signing_requests": []*domain.SigningRequest{retryReq},
	})
}

func (s *Server) advanceLeg2Retry(
	w http.ResponseWriter,
	r *http.Request,
	session *LiveSession,
	signed []domain.SignedAction,
	releaseDone <-chan struct{},
) {
	ctx := r.Context()
	retrySigned := findSigned(signed, session.Leg2RetryReqID)
	if retrySigned == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected signed leg-2 retry action (or abort:true)"})
		return
	}
	retryReq, err := s.live.signingStore.ValidateAndConsume(*retrySigned)
	if err != nil {
		s.recoverInvalidHedge(w, ctx, session, "leg-2 retry signature validation failed")
		return
	}

	session.Leg2Attempts = 2
	session.State = sessLeg2Submitting
	s.live.sessions.put(session)
	if err := s.saveLiveSession(ctx, session); err != nil {
		s.recoverInvalidHedge(w, ctx, session, "leg-2 retry persistence failed")
		return
	}
	sub, err := s.submitSignedAction(ctx, *retrySigned, retryReq)
	if err != nil || sub == nil {
		s.live.sessions.remove(session.ID)
		go func() {
			<-releaseDone
			s.recoverExposedSession(session, "leg-2 retry submission result was ambiguous")
		}()
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": session.ID, "status": string(sessRecovering),
			"reason": "leg-2 retry result is unknown; venue reconciliation started",
		})
		return
	}
	if !sub.Accepted {
		s.recoverInvalidHedge(w, ctx, session, "leg-2 retry rejected")
		return
	}
	session.State = sessLeg2Submitted
	if err := s.saveLiveSession(ctx, session); err != nil {
		s.logger.Error("live advance: persist accepted leg-2 retry", "err", err, "session_id", session.ID)
	}

	fillCtx, cancelFill := context.WithDeadline(ctx, retryReq.ExpiresAt)
	retryFill, err := s.waitForLegFill(fillCtx, retryReq)
	cancelFill()
	if err != nil || retryFill == nil {
		s.live.sessions.remove(session.ID)
		go func() {
			<-releaseDone
			s.recoverExposedSession(session, "leg-2 retry fill result was ambiguous")
		}()
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": session.ID, "status": string(sessRecovering),
			"reason": "leg-2 retry fill is unknown; venue reconciliation started",
		})
		return
	}
	session.Leg2Fill = mergeNormFills(session.Leg2Fill, retryFill)
	if err := s.saveLiveSession(ctx, session); err != nil {
		s.logger.Error("live advance: persist aggregate leg-2 fill", "err", err, "session_id", session.ID)
	}
	mismatch := hedgeMismatch(session.Leg1Fill.FilledAmount, session.Leg2Fill.FilledAmount)
	if mismatch <= maxHedgeMismatchPct {
		s.completeHedgeOpen(w, ctx, session, mismatch)
		return
	}
	s.recoverInvalidHedge(w, ctx, session,
		fmt.Sprintf("hedge mismatch %.2f%% after one retry", mismatch*100))
}

func (s *Server) completeHedgeOpen(w http.ResponseWriter, ctx context.Context, session *LiveSession, mismatch float64) {
	if len(session.Recovery) > 0 && session.Recovery[len(session.Recovery)-1].Action == "retry_leg2" {
		session.Recovery[len(session.Recovery)-1].Success = true
	}
	session.State = sessOpen
	s.live.sessions.remove(session.ID)
	positionID := s.persistSession(ctx, session, executor.ExecStateOpen)
	s.logger.Info("live advance: hedge open", "session_id", session.ID, "mismatch", mismatch, "position_id", positionID)
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": session.ID, "status": string(sessOpen),
		"leg1_fill": fillView(session.Leg1Fill, 0), "leg2_fill": fillView(session.Leg2Fill, 0),
		"mismatch": mismatch, "position_id": positionID,
	})
}

func (s *Server) recoverInvalidHedge(w http.ResponseWriter, ctx context.Context, session *LiveSession, reason string) {
	unwind := unwindResult{Status: unwindSkipped}
	leg1Amount := fillAmount(session.Leg1Fill)
	leg2Amount := fillAmount(session.Leg2Fill)
	if leg2Amount <= leg1Amount {
		unwind = s.fireUnwind(ctx, session)
		session.Leg1Fill = remainingFillAfterUnwind(session.Leg1Fill, unwind.FilledAmount)
		session.Recovery = append(session.Recovery, executor.RecoveryAction{
			Action: "unwind_leg1", Success: unwind.Confirmed(),
			Detail: fmt.Sprintf("filled=%.8f requested=%.8f status=%s", unwind.FilledAmount, unwind.RequestedAmount, unwind.Status),
		})
	} else {
		reason += "; automatic leg-1 unwind skipped because it would increase naked leg-2 exposure"
		session.Recovery = append(session.Recovery, executor.RecoveryAction{
			Action: "none", Detail: "overfilled leg 2 kept hedged for manual recovery",
		})
	}

	state := executor.ExecStateDegraded
	session.State = sessDegraded
	if session.Leg1Fill == nil && session.Leg2Fill == nil {
		state = executor.ExecStateFailed
		session.State = sessFailed
	}
	fullReason := reason + unwindReasonSuffix(unwind)
	positionID := s.persistSession(ctx, session, state, fullReason)
	response := map[string]any{
		"session_id": session.ID, "status": string(session.State),
		"leg1_fill": fillView(session.Leg1Fill, 0), "leg2_fill": fillView(session.Leg2Fill, 0),
		"mismatch": hedgeMismatch(fillAmount(session.Leg1Fill), fillAmount(session.Leg2Fill)),
		"reason":   fullReason, "position_id": positionID,
		"remaining_exposure": remainingExposureView(session),
	}
	for key, value := range unwindJSON(unwind) {
		response[key] = value
	}
	writeJSON(w, http.StatusOK, response)
}

func retryableLeg2Fill(fill *normFill, target float64) bool {
	if fill == nil || fill.FilledAmount >= target {
		return false
	}
	return fill.FilledAmount > 0 || fill.Status != "timeout"
}

func unwindFullyFilled(requested, filled float64) bool {
	if requested <= 0 {
		return false
	}
	tolerance := math.Max(1e-9, requested*1e-9)
	return filled+tolerance >= requested
}

func fillAmount(fill *normFill) float64 {
	if fill == nil {
		return 0
	}
	return fill.FilledAmount
}

func remainingExposureView(session *LiveSession) []map[string]any {
	remaining := make([]map[string]any, 0, 2)
	if session.Leg1Fill != nil {
		remaining = append(remaining, map[string]any{
			"leg": 1, "venue": session.Leg1.venue, "symbol": session.Leg1.symbol,
			"side": session.Leg1.side, "amount": session.Leg1Fill.FilledAmount,
		})
	}
	if session.Leg2Fill != nil {
		remaining = append(remaining, map[string]any{
			"leg": 2, "venue": session.Leg2.venue, "symbol": session.Leg2.symbol,
			"side": session.Leg2.side, "amount": session.Leg2Fill.FilledAmount,
		})
	}
	return remaining
}
