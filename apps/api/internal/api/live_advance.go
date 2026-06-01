package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

const (
	minHedgeableFillPct = 0.50
	maxHedgeMismatchPct = 0.05
)

// handleLiveAdvance drives the non-custodial two-leg open state machine.
//
// POST /api/v1/live/advance
//
// Input:
//
//	{
//	  "session_id": "...",
//	  "signed_actions": [ SignedAction, ... ],  // leg-1 open+unwind, then leg-2 open
//	  "abort": false                            // true = user aborted; fire armed unwind
//	}
//
// State machine:
//   - awaiting_leg1_signs: arm unwind, submit leg 1, wait fill, check >=50%.
//     under-fill or failure -> fire armed unwind -> aborted/failed.
//     ok -> return leg-2 signing request sized from actual leg-1 fill.
//   - awaiting_leg2_sign: submit leg 2, wait fill, check <=5% mismatch.
//     ok -> open. mismatch/failure/abort -> fire armed unwind -> degraded/aborted.
func (s *Server) handleLiveAdvance(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "live execution not configured",
		})
		return
	}

	var req struct {
		SessionID     string                `json:"session_id"`
		SignedActions []domain.SignedAction `json:"signed_actions"`
		Abort         bool                  `json:"abort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id required"})
		return
	}

	sess, ok := s.live.sessions.get(req.SessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found or expired"})
		return
	}

	switch sess.State {
	case sessAwaitingLeg1Signs:
		s.advanceLeg1(w, r, sess, req.SignedActions)
	case sessAwaitingLeg2Sign:
		if req.Abort {
			s.abortAfterLeg1(w, r, sess, "user aborted before leg 2 signing")
			return
		}
		s.advanceLeg2(w, r, sess, req.SignedActions)
	default:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "session already in terminal state",
			"status": string(sess.State),
		})
	}
}

// advanceLeg1 arms the unwind, submits leg 1, waits for fill, and either returns
// the leg-2 signing request (sized from the actual fill) or fires the unwind.
func (s *Server) advanceLeg1(w http.ResponseWriter, r *http.Request, sess *LiveSession, signed []domain.SignedAction) {
	ctx := r.Context()

	openSigned := findSigned(signed, sess.Leg1OpenReqID)
	unwindSigned := findSigned(signed, sess.Leg1UnwindReqID)
	if openSigned == nil || unwindSigned == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "expected signed leg-1 open and leg-1 unwind actions",
		})
		return
	}

	// Validate + consume both signed actions.
	openReq, err := s.live.signingStore.ValidateAndConsume(*openSigned)
	if err != nil {
		sess.State = sessFailed
		s.live.sessions.remove(sess.ID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "leg-1 open validation failed: " + err.Error()})
		return
	}
	unwindReq, err := s.live.signingStore.ValidateAndConsume(*unwindSigned)
	if err != nil {
		sess.State = sessFailed
		s.live.sessions.remove(sess.ID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "leg-1 unwind validation failed: " + err.Error()})
		return
	}

	// Arm the unwind — held, not submitted, until a failure needs it.
	sess.ArmedUnwindSigned = unwindSigned
	sess.ArmedUnwindReq = unwindReq

	// Submit leg 1.
	sub, err := s.submitSignedAction(ctx, *openSigned, openReq)
	if err != nil || sub == nil || !sub.Accepted {
		// Leg 1 never opened — nothing to unwind.
		sess.State = sessFailed
		s.live.sessions.remove(sess.ID)
		reason := "leg 1 submit rejected"
		if sub != nil && sub.Error != "" {
			reason = "leg 1 rejected: " + sub.Error
		} else if err != nil {
			reason = "leg 1 submit error: " + err.Error()
		}
		s.persistSession(ctx, sess, executor.ExecStateFailed, reason)
		s.logger.Error("live advance: leg 1 not accepted", "session_id", sess.ID, "reason", reason)
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sess.ID, "status": string(sessFailed), "reason": reason,
		})
		return
	}

	// Wait for leg 1 fill.
	fill, err := s.waitForLegFill(ctx, openReq)
	if err != nil || fill == nil {
		unwound := s.fireUnwind(ctx, sess)
		sess.State = sessAborted
		s.live.sessions.remove(sess.ID)
		s.persistSession(ctx, sess, executor.ExecStateFailed, "leg 1 fill wait failed; unwind fired")
		s.logger.Error("live advance: leg 1 fill wait failed", "session_id", sess.ID, "err", err, "unwound", unwound)
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sess.ID, "status": string(sessAborted),
			"reason": "leg 1 fill could not be confirmed", "unwound": unwound,
		})
		return
	}
	sess.Leg1Fill = fill

	fillRatio := 0.0
	if sess.Plan.Notional > 0 {
		fillRatio = fill.FilledAmount / sess.Plan.Notional
	}

	// Minimum hedgeable fill check.
	if fillRatio < minHedgeableFillPct {
		unwound := s.fireUnwind(ctx, sess)
		sess.State = sessAborted
		s.live.sessions.remove(sess.ID)
		reason := fmt.Sprintf("leg 1 underfilled %.1f%% (< %.0f%%); unwind fired", fillRatio*100, minHedgeableFillPct*100)
		s.persistSession(ctx, sess, executor.ExecStateFailed, reason)
		s.logger.Warn("live advance: leg 1 underfilled", "session_id", sess.ID, "fill_ratio", fillRatio, "unwound", unwound)
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sess.ID, "status": string(sessAborted),
			"leg1_fill": fillView(fill, fillRatio), "reason": reason, "unwound": unwound,
		})
		return
	}

	// Build leg-2 open sized from the actual leg-1 fill.
	leg2Cloid := fmt.Sprintf("orbital-l2open-%d", time.Now().UnixNano())
	leg2Open, err := s.buildOpenSigningRequest(sess.Leg2, fill.FilledAmount, leg2Cloid, sess.AccountPacifica)
	if err != nil {
		unwound := s.fireUnwind(ctx, sess)
		sess.State = sessDegraded
		s.live.sessions.remove(sess.ID)
		s.persistSession(ctx, sess, executor.ExecStateDegraded, "leg 2 payload build failed; unwind fired")
		s.logger.Error("live advance: leg 2 build failed", "session_id", sess.ID, "err", err, "unwound", unwound)
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sess.ID, "status": string(sessDegraded),
			"reason": "could not build hedge order; leg 1 unwound", "unwound": unwound,
		})
		return
	}

	s.live.signingStore.Store(leg2Open)
	sess.Leg2OpenReqID = leg2Open.ID
	sess.State = sessAwaitingLeg2Sign
	s.live.sessions.put(sess)

	s.logger.Info("live advance: leg 1 filled, leg 2 ready",
		"session_id", sess.ID, "leg1_filled", fill.FilledAmount, "fill_ratio", fillRatio)

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":       sess.ID,
		"status":           string(sessAwaitingLeg2Sign),
		"leg1_fill":        fillView(fill, fillRatio),
		"signing_requests": []*domain.SigningRequest{leg2Open},
	})
}

// advanceLeg2 submits leg 2, verifies hedge mismatch, and either marks the
// position open or fires the armed unwind and degrades.
func (s *Server) advanceLeg2(w http.ResponseWriter, r *http.Request, sess *LiveSession, signed []domain.SignedAction) {
	ctx := r.Context()

	openSigned := findSigned(signed, sess.Leg2OpenReqID)
	if openSigned == nil {
		// Malformed; keep the session so the frontend can resend or abort.
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "expected signed leg-2 open action (or abort:true)",
		})
		return
	}

	leg2Req, err := s.live.signingStore.ValidateAndConsume(*openSigned)
	if err != nil {
		unwound := s.fireUnwind(ctx, sess)
		sess.State = sessDegraded
		s.live.sessions.remove(sess.ID)
		s.persistSession(ctx, sess, executor.ExecStateDegraded, "leg 2 validation failed; unwind fired")
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sess.ID, "status": string(sessDegraded),
			"reason": "leg 2 validation failed; leg 1 unwound", "unwound": unwound,
		})
		return
	}

	// Submit leg 2.
	sub, err := s.submitSignedAction(ctx, *openSigned, leg2Req)
	if err != nil || sub == nil || !sub.Accepted {
		unwound := s.fireUnwind(ctx, sess)
		sess.State = sessDegraded
		s.live.sessions.remove(sess.ID)
		reason := "leg 2 submit rejected; leg 1 unwound"
		s.persistSession(ctx, sess, executor.ExecStateDegraded, reason)
		s.logger.Error("live advance: leg 2 not accepted", "session_id", sess.ID, "unwound", unwound)
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sess.ID, "status": string(sessDegraded),
			"leg1_fill": fillView(sess.Leg1Fill, 0), "reason": reason, "unwound": unwound,
		})
		return
	}

	// Wait for leg 2 fill.
	fill2, err := s.waitForLegFill(ctx, leg2Req)
	if err != nil || fill2 == nil {
		unwound := s.fireUnwind(ctx, sess)
		sess.State = sessDegraded
		s.live.sessions.remove(sess.ID)
		s.persistSession(ctx, sess, executor.ExecStateDegraded, "leg 2 fill wait failed; unwind fired")
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sess.ID, "status": string(sessDegraded),
			"leg1_fill": fillView(sess.Leg1Fill, 0), "reason": "leg 2 fill not confirmed; leg 1 unwound", "unwound": unwound,
		})
		return
	}
	sess.Leg2Fill = fill2

	leg1Amt := sess.Leg1Fill.FilledAmount
	mismatch := 1.0
	if leg1Amt > 0 {
		mismatch = math.Abs(fill2.FilledAmount-leg1Amt) / leg1Amt
	}

	// Success: leg 2 filled within the hedge mismatch band.
	if fill2.Filled && mismatch <= maxHedgeMismatchPct {
		sess.State = sessOpen
		s.live.sessions.remove(sess.ID)
		posID := s.persistSession(ctx, sess, executor.ExecStateOpen)
		s.logger.Info("live advance: hedge open", "session_id", sess.ID, "mismatch", mismatch, "position_id", posID)
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sess.ID, "status": string(sessOpen),
			"leg1_fill": fillView(sess.Leg1Fill, 0), "leg2_fill": fillView(fill2, 0),
			"mismatch": mismatch, "position_id": posID,
		})
		return
	}

	// Mismatch too high (or leg 2 unfilled) — unwind leg 1, degrade.
	unwound := s.fireUnwind(ctx, sess)
	sess.State = sessDegraded
	s.live.sessions.remove(sess.ID)
	reason := fmt.Sprintf("hedge mismatch %.2f%% (> %.0f%%); leg 1 unwound", mismatch*100, maxHedgeMismatchPct*100)
	posID := s.persistSession(ctx, sess, executor.ExecStateDegraded, reason)
	s.logger.Warn("live advance: hedge mismatch, degraded", "session_id", sess.ID, "mismatch", mismatch, "unwound", unwound)
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sess.ID, "status": string(sessDegraded),
		"leg1_fill": fillView(sess.Leg1Fill, 0), "leg2_fill": fillView(fill2, 0),
		"mismatch": mismatch, "reason": reason, "position_id": posID, "unwound": unwound,
	})
}

// abortAfterLeg1 fires the armed unwind when the user aborts before signing leg 2.
func (s *Server) abortAfterLeg1(w http.ResponseWriter, r *http.Request, sess *LiveSession, reason string) {
	ctx := r.Context()
	unwound := s.fireUnwind(ctx, sess)
	sess.State = sessAborted
	s.live.sessions.remove(sess.ID)
	s.persistSession(ctx, sess, executor.ExecStateFailed, reason+"; leg 1 unwound")
	s.logger.Warn("live advance: aborted after leg 1", "session_id", sess.ID, "unwound", unwound)
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sess.ID, "status": string(sessAborted),
		"reason": reason, "unwound": unwound,
	})
}

// fireUnwind submits the pre-signed reduce-only leg-1 unwind and best-effort
// waits for its fill. Reduce-only auto-caps to the actual open size.
func (s *Server) fireUnwind(ctx context.Context, sess *LiveSession) bool {
	if sess.ArmedUnwindSigned == nil || sess.ArmedUnwindReq == nil {
		s.logger.Warn("live advance: no armed unwind to fire", "session_id", sess.ID)
		return false
	}
	sub, err := s.submitSignedAction(ctx, *sess.ArmedUnwindSigned, sess.ArmedUnwindReq)
	ok := err == nil && sub != nil && sub.Accepted
	if !ok {
		s.logger.Error("live advance: unwind submit failed", "session_id", sess.ID, "err", err)
		return false
	}
	// Best-effort fill confirmation; ignore result.
	_, _ = s.waitForLegFill(ctx, sess.ArmedUnwindReq)
	s.logger.Info("live advance: armed unwind fired", "session_id", sess.ID)
	return true
}

// submitSignedAction routes a signed action to the right venue client.
func (s *Server) submitSignedAction(
	ctx context.Context, signed domain.SignedAction, req *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	switch signed.Venue {
	case "pacifica":
		if s.live.pacClient == nil {
			return nil, fmt.Errorf("pacifica live client not configured")
		}
		return s.live.pacClient.SubmitSignedOrder(ctx, signed, req, s.live.pacTracker)
	case "hyperliquid":
		if s.live.hlClient == nil {
			return nil, fmt.Errorf("hyperliquid live client not configured")
		}
		return s.live.hlClient.SubmitSignedOrder(ctx, signed, req)
	default:
		return nil, fmt.Errorf("unsupported venue: %s", signed.Venue)
	}
}

// waitForLegFill blocks on the venue tracker for the order's terminal fill state.
func (s *Server) waitForLegFill(ctx context.Context, req *domain.SigningRequest) (*normFill, error) {
	switch req.Venue {
	case "pacifica":
		fr, err := s.live.pacTracker.WaitForFill(ctx, req.ClientOrderID)
		if err != nil {
			return nil, err
		}
		return &normFill{
			FilledAmount: fr.FilledAmount, AvgFillPrice: fr.AvgFillPrice, Fee: fr.TotalFee,
			OrderID: fr.OrderID, Status: string(fr.Status),
			Filled: fr.Status == paclive.OrderStatusFilled || fr.Status == paclive.OrderStatusPartialFill,
		}, nil
	case "hyperliquid":
		var meta struct {
			Cloid string `json:"cloid"`
		}
		if err := json.Unmarshal(req.VenueMetadata, &meta); err != nil {
			return nil, fmt.Errorf("parse hl venue metadata: %w", err)
		}
		fr, err := s.live.hlClient.WaitForFill(ctx, meta.Cloid)
		if err != nil {
			return nil, err
		}
		return &normFill{
			FilledAmount: fr.FilledAmount, AvgFillPrice: fr.AvgFillPrice, Fee: fr.TotalFee,
			OrderID: fr.OrderID, Status: string(fr.Status),
			Filled: fr.Status == hllive.OrderStatusFilled || fr.Status == hllive.OrderStatusPartialFill,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported venue: %s", req.Venue)
	}
}

// persistSession writes the session's final outcome to the live store so it
// appears in /live/positions and is tracked by the monitor / kill switch.
// Returns the persisted position ID (plan ID).
func (s *Server) persistSession(ctx context.Context, sess *LiveSession, state executor.ExecState, reasons ...string) string {
	if s.live.liveStore == nil {
		return ""
	}
	res := &executor.ExecutionResult{
		OpportunityID: sess.Plan.OpportunityID,
		PlanID:        sess.Plan.ID,
		Asset:         sess.Plan.Asset,
		State:         state,
		Leg1:          legResultFrom(sess.Leg1, sess.Leg1Fill, sess.Leg1OpenReqID, sess.Plan.Notional),
		Leg2:          legResultFrom(sess.Leg2, sess.Leg2Fill, sess.Leg2OpenReqID, leg1FilledAmount(sess)),
		Reasons:       reasons,
		StartedAt:     sess.CreatedAt,
		CompletedAt:   time.Now(),
	}
	s.live.liveStore.PersistFullResult(
		ctx, res, sess.Plan.Leg1.Venue, sess.Plan.Leg2.Venue,
		sess.Plan.Notional, sess.Plan.Leverage.Leverage,
	)
	return sess.Plan.ID
}

func leg1FilledAmount(sess *LiveSession) float64 {
	if sess.Leg1Fill != nil {
		return sess.Leg1Fill.FilledAmount
	}
	return 0
}

func legResultFrom(leg legPlan, fill *normFill, clientOrderID string, requestedAmt float64) executor.LegResult {
	lr := executor.LegResult{
		Venue:         leg.venue,
		Symbol:        leg.symbol,
		Side:          string(leg.side),
		ClientOrderID: clientOrderID,
		RequestedAmt:  requestedAmt,
	}
	if fill != nil {
		lr.Submitted = true
		lr.Accepted = true
		lr.Filled = fill.Filled
		lr.FilledAmount = fill.FilledAmount
		lr.AvgFillPrice = fill.AvgFillPrice
		lr.Fee = fill.Fee
		lr.OrderID = fill.OrderID
		if requestedAmt > 0 {
			lr.FillRatio = fill.FilledAmount / requestedAmt
		}
	}
	return lr
}

func findSigned(actions []domain.SignedAction, reqID string) *domain.SignedAction {
	for i := range actions {
		if actions[i].RequestID == reqID {
			return &actions[i]
		}
	}
	return nil
}

func fillView(f *normFill, ratio float64) map[string]any {
	if f == nil {
		return nil
	}
	v := map[string]any{
		"filled_amount": f.FilledAmount,
		"avg_price":     f.AvgFillPrice,
		"status":        f.Status,
	}
	if ratio > 0 {
		v["fill_ratio"] = ratio
	}
	return v
}
