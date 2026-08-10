package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

const (
	livePrepareAccountStateNotReady  = "ACCOUNT_STATE_NOT_READY"
	livePreparePositionStateNotReady = "POSITION_STATE_NOT_READY"
	livePrepareExistingPosition      = "EXISTING_POSITION"
	livePrepareExistingSession       = "EXISTING_SESSION"
	livePreparePreTradeBlocked       = "PRETRADE_BLOCKED"
)

// handleLivePrepare builds unsigned signing requests for a live trade.
//
// POST /api/v1/live/prepare
//
// Input:
//
//	{
//	  "opportunity_id": "...",
//	  "leverage": 2.0,
//	  "account_pacifica": "...",   // Solana pubkey for Pacifica
//	  "account_hyperliquid": "..." // Ethereum address for Hyperliquid
//	}
//
// Returns two signing requests (one per leg) or an error.
func (s *Server) handleLivePrepare(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "live execution not configured",
		})
		return
	}
	s.cleanupExpiredLiveSessions()

	var req struct {
		OpportunityID      string   `json:"opportunity_id"`
		Leverage           float64  `json:"leverage"`
		RequestedNotional  *float64 `json:"requested_notional,omitempty"`
		AccountPacifica    string   `json:"account_pacifica"`
		AccountHyperliquid string   `json:"account_hyperliquid"`
		AgentPacifica      string   `json:"agent_pacifica"`
		AgentHyperliquid   string   `json:"agent_hyperliquid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.OpportunityID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "opportunity_id required"})
		return
	}
	if req.AccountPacifica == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_pacifica required"})
		return
	}
	if req.AccountHyperliquid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_hyperliquid required"})
		return
	}
	if req.AgentPacifica == "" || req.AgentHyperliquid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_pacifica and agent_hyperliquid required"})
		return
	}
	if err := s.live.validateAgentIdentity("pacifica", req.AccountPacifica, req.AgentPacifica); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.live.validateAgentIdentity("hyperliquid", req.AccountHyperliquid, req.AgentHyperliquid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.RequestedNotional != nil && *req.RequestedNotional <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "requested_notional must be positive"})
		return
	}
	var notional float64
	if req.RequestedNotional != nil {
		notional = *req.RequestedNotional
	}
	// 1. Build a fresh execution plan
	plan, err := s.scanner.BuildPlan(r.Context(), req.OpportunityID, req.Leverage, notional)
	if err != nil {
		s.logger.Error("live prepare: build plan failed", "err", err)
		writePlanError(w, http.StatusUnprocessableEntity, err)
		return
	}
	if !plan.Executable {
		s.logger.Warn("live prepare: plan not executable",
			"opportunity_id", req.OpportunityID,
			"warnings", plan.Warnings,
		)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "plan not executable",
		})
		return
	}

	// 2. Find the opportunity for admission gate
	opp := s.scanner.FindOpportunity(req.OpportunityID)
	if opp == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "opportunity not found"})
		return
	}

	// 3. Acquire account-scoped feeds. Different wallet pairs can prepare in
	// parallel, while the same accounts remain serialized through this flow.
	accounts, err := s.live.acquireAccounts(req.AccountPacifica, req.AccountHyperliquid)
	if err != nil {
		s.logger.Warn("live prepare: account feeds unavailable", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	defer accounts.Release()
	unlockAccounts := accounts.Lock()
	defer unlockAccounts()
	req.AccountPacifica = accountSnapshot(accounts, "pacifica").Account
	req.AccountHyperliquid = accountSnapshot(accounts, "hyperliquid").Account
	authorized, err := s.live.agentPairAuthorizationMatches(
		r.Context(), req.AccountPacifica, req.AccountHyperliquid, req.AgentPacifica, req.AgentHyperliquid,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify trading agent authorization"})
		return
	}
	if !authorized {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "trading agent authorization not registered; reauthorize both agents"})
		return
	}

	// 3b. Account-data readiness gate. This is separate from the admission
	// gate below because admission looks at policy (leverage caps, etc); this
	// looks at whether we have current account state to submit against. On
	// first prepare after connect, streams may not have produced a snapshot
	// yet — return 409 with a clear per-venue reason so the UI can retry.
	pacStatus, hlStatus := liveAccountStatuses(accounts, admissionFreshness)
	var notReady []string
	if !pacStatus.Fresh {
		notReady = append(notReady, fmt.Sprintf("Pacifica: %s", pacStatus.Reason))
	}
	if !hlStatus.Fresh {
		notReady = append(notReady, fmt.Sprintf("Hyperliquid: %s", hlStatus.Reason))
	}
	if len(notReady) > 0 {
		s.logger.Warn("live prepare: account state not ready",
			"code", livePrepareAccountStateNotReady,
			"opportunity_id", req.OpportunityID,
			"reasons", notReady,
			"pacifica_connected", pacStatus.Connected,
			"pacifica_stream_ready", pacStatus.StreamReady,
			"pacifica_age_seconds", pacStatus.AgeSeconds,
			"hyperliquid_connected", hlStatus.Connected,
			"hyperliquid_stream_ready", hlStatus.StreamReady,
			"hyperliquid_age_seconds", hlStatus.AgeSeconds,
		)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "account state not ready",
			"code":    livePrepareAccountStateNotReady,
			"reasons": notReady,
		})
		return
	}
	pacPositionReady := venuePositionStateReady(accounts, "pacifica")
	hlPositionReady := venuePositionStateReady(accounts, "hyperliquid")
	if !pacPositionReady || !hlPositionReady {
		var reasons []string
		if !pacPositionReady {
			reasons = append(reasons, "Pacifica: position state not yet received")
		}
		if !hlPositionReady {
			reasons = append(reasons, "Hyperliquid: position state not yet received")
		}
		s.logger.Warn("live prepare: position state not ready",
			"code", livePreparePositionStateNotReady,
			"opportunity_id", req.OpportunityID,
			"reasons", reasons,
			"pacifica_positions_updated_at", accountSnapshot(accounts, "pacifica").PositionsUpdatedAt,
			"hyperliquid_positions_updated_at", accountSnapshot(accounts, "hyperliquid").PositionsUpdatedAt,
		)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "venue position state not yet received; retry shortly",
			"code":    livePreparePositionStateNotReady,
			"reasons": reasons,
		})
		return
	}

	// 4. Live admission gate
	admission := domain.CheckLiveAdmission(*opp, plan.Leverage.Leverage, float64(plan.MaxLeverage))
	if !admission.Allowed {
		s.logger.Warn("live prepare: admission denied",
			"asset", opp.Asset,
			"reasons", admission.Reasons,
		)
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":   "live admission denied",
			"reasons": admission.Reasons,
		})
		return
	}
	if reasons := livePreTradeBlockers(plan, accounts); len(reasons) > 0 {
		s.logger.Warn("live prepare: pre-trade blocked",
			"code", livePreparePreTradeBlocked,
			"opportunity_id", req.OpportunityID,
			"reasons", reasons,
		)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "pre-trade checks failed",
			"code":    livePreparePreTradeBlocked,
			"reasons": reasons,
		})
		return
	}

	// 4. Riskier leg first (higher slippage = thinner book → submit first).
	leg1, leg2 := orderLegsByRisk(plan)
	leg1Amount, err := liveBaseAmount(plan.Notional, leg1.price)
	if err != nil {
		s.logger.Error("live prepare: calculate base amount", "err", err, "notional", plan.Notional, "price", leg1.price)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "could not size live order from dollar notional"})
		return
	}
	baselineLeg1Size, _ := currentVenuePosition(accounts, leg1.venue, leg1.symbol)
	baselineLeg2Size, _ := currentVenuePosition(accounts, leg2.venue, leg2.symbol)
	if math.Abs(baselineLeg1Size) > 1e-9 || math.Abs(baselineLeg2Size) > 1e-9 {
		s.logger.Warn("live prepare: existing position blocks open",
			"code", livePrepareExistingPosition,
			"opportunity_id", req.OpportunityID,
			"asset", opp.Asset,
			"leg1_venue", leg1.venue,
			"leg1_size", baselineLeg1Size,
			"leg2_venue", leg2.venue,
			"leg2_size", baselineLeg2Size,
		)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "existing position for this asset must be closed before starting a live session",
			"code":  livePrepareExistingPosition,
		})
		return
	}
	superseded, err := s.liveStore.SupersedeSafeDurableSessions(
		r.Context(), req.AccountPacifica, req.AccountHyperliquid, plan.Asset,
	)
	if err != nil {
		s.logger.Error("live prepare: supersede safe sessions", "err", err, "asset", plan.Asset)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare live session slot"})
		return
	}
	for _, sessionID := range superseded {
		s.live.sessions.remove(sessionID)
	}
	activeSession, err := s.liveStore.ActiveDurableSessionForAccountAsset(
		r.Context(), req.AccountPacifica, req.AccountHyperliquid, plan.Asset,
	)
	if err == nil {
		s.logger.Warn("live prepare: existing session blocks open",
			"code", livePrepareExistingSession,
			"session_id", activeSession.ID,
			"state", activeSession.State,
			"has_exposure", activeSession.HasExposure,
		)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "existing live session for this asset requires recovery before retrying",
			"code":       livePrepareExistingSession,
			"session_id": activeSession.ID,
			"state":      activeSession.State,
		})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("live prepare: inspect active session", "err", err, "asset", plan.Asset)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to inspect live session slot"})
		return
	}

	// 5. Build leg-1 OPEN + leg-1 reduce-only UNWIND signing requests.
	// Both are on the riskier leg's venue/wallet and signed together up front,
	// so the backend holds a signature-free escape the moment leg 1 fills.
	now := time.Now()
	leg1OpenCloid := fmt.Sprintf("orbital-l1open-%d", now.UnixNano())
	leg1UnwindCloid := fmt.Sprintf("orbital-l1unwind-%d", now.UnixNano()+1)

	leg1Open, err := s.buildOpenSigningRequest(
		leg1, leg1Amount, leg1OpenCloid, req.AccountPacifica, req.AccountHyperliquid,
		req.AgentPacifica, req.AgentHyperliquid,
	)
	if err != nil {
		s.logger.Error("live prepare: build leg1 open", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("leg1 open payload build failed: %s", err),
		})
		return
	}

	leg1Unwind, err := s.buildUnwindSigningRequest(
		leg1, leg1Amount, leg1UnwindCloid, req.AccountPacifica, req.AccountHyperliquid,
		req.AgentPacifica, req.AgentHyperliquid,
	)
	if err != nil {
		s.logger.Error("live prepare: build leg1 unwind", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("leg1 unwind payload build failed: %s", err),
		})
		return
	}

	s.live.signingStore.Store(leg1Open)
	s.live.signingStore.Store(leg1Unwind)

	// 6. Create the orchestration session.
	sessionID := uuid.New().String()
	sess := &LiveSession{
		ID:                 sessionID,
		Plan:               plan,
		Leg1:               leg1,
		Leg2:               leg2,
		AccountPacifica:    req.AccountPacifica,
		AccountHyperliquid: req.AccountHyperliquid,
		AgentPacifica:      req.AgentPacifica,
		AgentHyperliquid:   req.AgentHyperliquid,
		State:              sessAwaitingLeg1Signs,
		Leg1OpenReqID:      leg1Open.ID,
		Leg1UnwindReqID:    leg1Unwind.ID,
		Leg1OpenReq:        leg1Open,
		Leg1UnwindReq:      leg1Unwind,
		BaselineLeg1Size:   baselineLeg1Size,
		BaselineLeg2Size:   baselineLeg2Size,
		CreatedAt:          now,
	}
	s.live.sessions.put(sess)
	if err := s.saveLiveSession(r.Context(), sess); err != nil {
		s.live.sessions.remove(sess.ID)
		s.logger.Error("live prepare: persist session", "err", err, "session_id", sess.ID)
		activeSession, activeErr := s.liveStore.ActiveDurableSessionForAccountAsset(
			r.Context(), req.AccountPacifica, req.AccountHyperliquid, plan.Asset,
		)
		if activeErr == nil && activeSession.ID != sess.ID {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":      "another live session for this asset started concurrently",
				"code":       livePrepareExistingSession,
				"session_id": activeSession.ID,
				"state":      activeSession.State,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist live session"})
		return
	}

	s.logger.Info("live prepare: session ready",
		"session_id", sessionID,
		"asset", opp.Asset,
		"riskier_venue", leg1.venue,
		"hedge_venue", leg2.venue,
		"plan_id", plan.ID,
	)

	// 7. Return session + leg-1 open and unwind signing requests.
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":       sessionID,
		"plan_id":          plan.ID,
		"asset":            plan.Asset,
		"notional":         plan.Notional,
		"leverage":         plan.Leverage,
		"riskier_venue":    leg1.venue,
		"hedge_venue":      leg2.venue,
		"expires_at":       leg1Open.ExpiresAt,
		"signing_requests": []*domain.SigningRequest{leg1Open, leg1Unwind},
	})
}

func liveBaseAmount(notional, price float64) (float64, error) {
	if notional <= 0 || math.IsNaN(notional) || math.IsInf(notional, 0) {
		return 0, fmt.Errorf("invalid notional: %v", notional)
	}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, fmt.Errorf("invalid price: %v", price)
	}
	return notional / price, nil
}

func liveSessionLeg1Amount(session *LiveSession) float64 {
	if session != nil && session.Leg1OpenReq != nil {
		return session.Leg1OpenReq.Amount
	}
	return 0
}

func liveSessionLeg2Amount(session *LiveSession) float64 {
	if session != nil && session.Leg2OpenReq != nil {
		return session.Leg2OpenReq.Amount
	}
	return 0
}

func livePreTradeBlockers(plan *domain.ExecutionPlan, accounts *liveAccountContext) []string {
	if plan == nil || accounts == nil {
		return []string{"live execution not configured"}
	}
	var blockers []string
	for _, leg := range []domain.Leg{plan.Leg1, plan.Leg2} {
		feed, ok := accounts.Feed(leg.Venue)
		if !ok {
			blockers = append(blockers, fmt.Sprintf("%s: unsupported venue", leg.Venue))
			continue
		}
		for _, reason := range feed.PreTradeBlockers(leg) {
			blockers = append(blockers, venueDisplayName(leg.Venue)+": "+reason)
		}
	}
	return blockers
}

func venueDisplayName(venue string) string {
	if venue == "" {
		return "Venue"
	}
	return strings.ToUpper(venue[:1]) + venue[1:]
}

// orderLegsByRisk resolves riskier-first ordering: the leg with higher slippage
// (thinner book) is submitted first. Mirrors executor.orderLegs.
func orderLegsByRisk(plan *domain.ExecutionPlan) (legPlan, legPlan) {
	a := legPlan{venue: plan.Leg1.Venue, symbol: plan.Leg1.Asset, side: plan.Leg1.Side, price: plan.Leg1.ExpectedPrice}
	b := legPlan{venue: plan.Leg2.Venue, symbol: plan.Leg2.Asset, side: plan.Leg2.Side, price: plan.Leg2.ExpectedPrice}
	if plan.Leg1.Slippage >= plan.Leg2.Slippage {
		return a, b
	}
	return b, a
}

// buildOpenSigningRequest builds an open-order signing request for one leg.
// accountPacifica is only used for Pacifica; Hyperliquid derives the account
// from the signature at submit time.
func (s *Server) buildOpenSigningRequest(
	leg legPlan, amount float64, clientOrderID, accountPacifica, accountHyperliquid,
	agentPacifica, agentHyperliquid string,
) (*domain.SigningRequest, error) {
	var request *domain.SigningRequest
	var err error
	switch leg.venue {
	case "pacifica":
		request, err = paclive.BuildOpenPayload(accountPacifica, leg.symbol, leg.side, amount, leg.price, clientOrderID)
	case "hyperliquid":
		if s.live.hlAssetMap == nil {
			return nil, fmt.Errorf("hyperliquid asset map not configured")
		}
		request, err = hllive.BuildOpenPayload(s.live.hlAssetMap, leg.symbol, leg.side, amount, leg.price, clientOrderID)
	default:
		return nil, fmt.Errorf("unsupported venue: %s", leg.venue)
	}
	if err == nil {
		request.Account = accountForVenue(leg.venue, accountPacifica, accountHyperliquid)
		request.Signer = signerForVenue(leg.venue, agentPacifica, agentHyperliquid)
		if request.Signer == "" {
			return nil, fmt.Errorf("%s trading agent required", leg.venue)
		}
	}
	return request, err
}

// buildUnwindSigningRequest builds a reduce-only close signing request for one leg.
// Side is the position side; the close payload inverts it internally.
func (s *Server) buildUnwindSigningRequest(
	leg legPlan, amount float64, clientOrderID, accountPacifica, accountHyperliquid,
	agentPacifica, agentHyperliquid string,
) (*domain.SigningRequest, error) {
	var request *domain.SigningRequest
	var err error
	switch leg.venue {
	case "pacifica":
		request, err = paclive.BuildClosePayload(accountPacifica, leg.symbol, leg.side, amount, leg.price, clientOrderID)
	case "hyperliquid":
		if s.live.hlAssetMap == nil {
			return nil, fmt.Errorf("hyperliquid asset map not configured")
		}
		request, err = hllive.BuildClosePayload(s.live.hlAssetMap, leg.symbol, leg.side, amount, leg.price, clientOrderID)
	default:
		return nil, fmt.Errorf("unsupported venue: %s", leg.venue)
	}
	if err == nil {
		request.Account = accountForVenue(leg.venue, accountPacifica, accountHyperliquid)
		request.Signer = signerForVenue(leg.venue, agentPacifica, agentHyperliquid)
		if request.Signer == "" {
			return nil, fmt.Errorf("%s trading agent required", leg.venue)
		}
	}
	return request, err
}

// handleLiveSubmit accepts a user-signed venue action and submits it.
//
// POST /api/v1/live/submit
//
// Restricted to close/unwind actions only. Live opens must go through the
// session flow (prepare → advance → advance) which enforces the two-leg
// state machine. This endpoint serves the kill switch and manual close paths.
//
// Input: SignedAction JSON
// Returns: SubmissionResult or error.
func (s *Server) handleLiveSubmit(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "live execution not configured",
		})
		return
	}

	var signed domain.SignedAction
	if err := json.NewDecoder(r.Body).Decode(&signed); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// 1. Validate against stored signing request (atomic — prevents double-submit)
	sigReq, err := s.live.signingStore.ValidateAndConsume(signed)
	if err != nil {
		s.logger.Warn("live submit: validation failed",
			"request_id", signed.RequestID,
			"venue", signed.Venue,
			"err", err,
		)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("validation failed: %s", err),
		})
		return
	}

	// 1b. Reject open actions — live opens must go through /live/advance.
	if sigReq.Action != "close" || !sigReq.ReduceOnly {
		s.logger.Warn("live submit: rejected non-close action",
			"request_id", signed.RequestID,
			"action", sigReq.Action,
			"reduce_only", sigReq.ReduceOnly,
		)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "live opens must use /api/v1/live/prepare and /api/v1/live/advance",
		})
		return
	}

	// 2. Submit through venue-specific path
	result, err := s.submitSignedAction(r.Context(), signed, sigReq)

	if err != nil {
		if errors.Is(err, errLiveSubmissionNotSent) {
			s.live.signingStore.Store(sigReq)
			s.logger.Warn("live submit: request not sent", "request_id", signed.RequestID, "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		s.trackAmbiguousCloseSubmission(sigReq, err.Error())
		s.logger.Error("live submit: submission error",
			"request_id", signed.RequestID,
			"venue", signed.Venue,
			"err", err,
		)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"request_id": sigReq.ID, "client_order_id": sigReq.ClientOrderID,
			"venue": sigReq.Venue, "accepted": false, "uncertain": true,
			"error": fmt.Sprintf("submission response uncertain: %s", err),
		})
		return
	}

	if result == nil {
		s.trackAmbiguousCloseSubmission(sigReq, "submission returned no result")
		s.logger.Error("live submit: nil result without error",
			"request_id", signed.RequestID,
			"venue", signed.Venue,
		)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"request_id": sigReq.ID, "client_order_id": sigReq.ClientOrderID,
			"venue": sigReq.Venue, "accepted": false, "uncertain": true,
			"error": "submission response uncertain: no result",
		})
		return
	}

	// 3. Log outcome
	if result.Accepted {
		s.logger.Info("live submit: order accepted",
			"venue", result.Venue,
			"order_id", result.OrderID,
			"client_order_id", result.ClientOrderID,
		)
	} else {
		s.logger.Warn("live submit: order rejected by venue",
			"venue", result.Venue,
			"client_order_id", result.ClientOrderID,
			"error", result.Error,
		)
	}
	s.trackCloseSubmission(sigReq, result)

	writeJSON(w, http.StatusOK, result)
}

// Two freshness thresholds serve two different needs:
//
//	admissionFreshness — hard gate at /live/prepare and pretrade checks in
//	  venue/*/account/pretrade.go. Must stay strict; this is what prevents
//	  submitting orders against stale account state.
//
//	displayFreshness — soft gate for the balances/readiness UI. Push-driven
//	  Pacifica WS doesn't heartbeat when nothing is happening, so a healthy
//	  but quiet account naturally ages past 30s. Using the strict window for
//	  display caused frequent false "Stale" flags with no user-visible fix.
//	  5 minutes is generous enough to hide the quiet-account case while
//	  still catching a genuinely broken stream.
const (
	admissionFreshness                = 30 * time.Second
	displayFreshness                  = 5 * time.Minute
	positionReconciliationQuietPeriod = 10 * time.Second
)

// venueAccountStatus is the per-venue account-data readiness view returned by
// /live/balances and consumed by the /live/prepare gate.
type venueAccountStatus struct {
	Venue       string     `json:"venue"`
	Equity      float64    `json:"equity"`
	Available   float64    `json:"available"`
	Connected   bool       `json:"connected"`    // wallet-derived stream is up
	StreamReady bool       `json:"stream_ready"` // account snapshot has been populated at least once
	Fresh       bool       `json:"fresh"`        // snapshot age within liveAccountFreshness
	LastUpdated *time.Time `json:"last_updated,omitempty"`
	AgeSeconds  float64    `json:"age_seconds"`
	Reason      string     `json:"reason,omitempty"` // human explanation when not ready
}

// buildVenueAccountStatus derives the readiness view from a raw venue snapshot.
// streamReady means the subscriber produced at least one snapshot (LastUpdated
// is non-zero); fresh means that snapshot is within liveAccountFreshness.
// The reason string is populated only when NOT fresh, so happy-path responses
// stay lean.
func buildVenueAccountStatus(venue string, connected bool, lastUpdated time.Time, equity, available float64, freshness time.Duration) venueAccountStatus {
	st := venueAccountStatus{
		Venue:     venue,
		Equity:    equity,
		Available: available,
		Connected: connected,
	}
	if !lastUpdated.IsZero() {
		lu := lastUpdated
		st.LastUpdated = &lu
		age := time.Since(lastUpdated)
		st.AgeSeconds = age.Seconds()
		st.StreamReady = true
		st.Fresh = age <= freshness
	}
	if !st.Connected {
		st.Reason = "account stream not connected"
	} else if !st.StreamReady {
		st.Reason = "account state not yet received"
	} else if !st.Fresh {
		st.Reason = fmt.Sprintf("account state stale (%.0fs old)", st.AgeSeconds)
	}
	return st
}

func liveAccountStatuses(accounts *liveAccountContext, freshness time.Duration) (venueAccountStatus, venueAccountStatus) {
	return accountStatus(accounts, "pacifica", freshness), accountStatus(accounts, "hyperliquid", freshness)
}

func accountStatus(accounts *liveAccountContext, venue string, freshness time.Duration) venueAccountStatus {
	snapshot := accountSnapshot(accounts, venue)
	return buildVenueAccountStatus(
		venue, snapshot.Connected, snapshot.LastUpdated,
		snapshot.Equity, snapshot.Available, freshness,
	)
}

func accountSnapshot(accounts *liveAccountContext, venue string) liveAccountSnapshot {
	feed, ok := accounts.Feed(venue)
	if !ok {
		return liveAccountSnapshot{Venue: venue}
	}
	return feed.Snapshot()
}

// handleLiveBalances returns per-venue account status: balances plus stream
// readiness and freshness. Never 500s if wallets aren't connected — returns
// a disconnected/zero response with a reason so the UI can render it.
// Zero equity/available is NOT treated as pending; freshness is the only
// gate on "Ready".
//
// GET /api/v1/live/balances?account_pacifica=...&account_hyperliquid=...
func (s *Server) handleLiveBalances(w http.ResponseWriter, r *http.Request) {
	pac, hl := s.liveAccountStatusesFor(
		r.URL.Query().Get("account_pacifica"),
		r.URL.Query().Get("account_hyperliquid"),
		displayFreshness,
	)
	writeJSON(w, http.StatusOK, map[string]venueAccountStatus{
		"pacifica":    pac,
		"hyperliquid": hl,
	})
}

func (s *Server) liveAccountStatusesFor(pacAccount, hlAddress string, freshness time.Duration) (venueAccountStatus, venueAccountStatus) {
	unavailable := func(venue string) venueAccountStatus {
		status := buildVenueAccountStatus(venue, false, time.Time{}, 0, 0, freshness)
		status.Reason = "account streams not active for requested wallets"
		return status
	}
	if s.live == nil || s.live.accounts == nil {
		return unavailable("pacifica"), unavailable("hyperliquid")
	}

	pacLease, pacFound := s.live.accounts.Lookup("pacifica", pacAccount)
	if pacFound {
		defer pacLease.Release()
	}
	hlLease, hlFound := s.live.accounts.Lookup("hyperliquid", hlAddress)
	if hlFound {
		defer hlLease.Release()
	}
	if !pacFound || !hlFound {
		pac := unavailable("pacifica")
		hl := unavailable("hyperliquid")
		if pacFound {
			snapshot := pacLease.Feed().Snapshot()
			pac = buildVenueAccountStatus("pacifica", snapshot.Connected, snapshot.LastUpdated, snapshot.Equity, snapshot.Available, freshness)
		}
		if hlFound {
			snapshot := hlLease.Feed().Snapshot()
			hl = buildVenueAccountStatus("hyperliquid", snapshot.Connected, snapshot.LastUpdated, snapshot.Equity, snapshot.Available, freshness)
		}
		return pac, hl
	}
	accounts := &liveAccountContext{leases: map[string]*accountFeedLease{
		"pacifica": pacLease, "hyperliquid": hlLease,
	}}
	return liveAccountStatuses(accounts, freshness)
}

// handleLiveAccountsEnsure starts the venue account subscribers up front so
// readiness can transition to "ready" BEFORE the user clicks Execute Live.
// Without this, /live/prepare was the only trigger for EnsureAccountStreams,
// which deadlocked the UI (readiness blocks prepare; prepare starts streams).
//
// POST /api/v1/live/accounts/ensure
// Body: {"account_pacifica": "...", "account_hyperliquid": "..."}
//
// The handler intentionally does NOT build a plan, create a session, or
// return signing requests — those remain on /live/prepare.
func (s *Server) handleLiveAccountsEnsure(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "live execution not configured",
		})
		return
	}
	var req struct {
		AccountPacifica    string `json:"account_pacifica"`
		AccountHyperliquid string `json:"account_hyperliquid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.AccountPacifica == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_pacifica required"})
		return
	}
	if req.AccountHyperliquid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_hyperliquid required"})
		return
	}

	accounts, err := s.live.acquireAccounts(req.AccountPacifica, req.AccountHyperliquid)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	defer accounts.Release()

	// Snapshot readiness right after the call. On a very first ensure the
	// snapshot will typically still be empty (streams start asynchronously);
	// the frontend polls /live/balances to see them go ready.
	pac, hl := liveAccountStatuses(accounts, displayFreshness)
	writeJSON(w, http.StatusOK, map[string]any{
		"pacifica":    pac,
		"hyperliquid": hl,
	})
}

// handleLivePositions returns all live positions, newest first.
//
// GET /api/v1/live/positions
func (s *Server) handleLivePositions(w http.ResponseWriter, r *http.Request) {
	pacificaAccount, hyperliquidAccount, ok := liveAccountsFromQuery(w, r)
	if !ok {
		return
	}
	positions, err := s.liveStore.ListPositionsForAccounts(r.Context(), pacificaAccount, hyperliquidAccount)
	if err != nil {
		s.logger.Error("live positions: list failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to list live positions",
		})
		return
	}

	if positions == nil {
		positions = []executor.LivePosition{}
	}
	writeJSON(w, http.StatusOK, positions)
}

// handleLivePosition returns a single live position with fills and events.
//
// GET /api/v1/live/positions/{id}
func (s *Server) handleLivePosition(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/live/positions/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}

	pacificaAccount, hyperliquidAccount, ok := liveAccountsFromQuery(w, r)
	if !ok {
		return
	}
	pos, err := s.liveStore.GetPositionForAccounts(r.Context(), id, pacificaAccount, hyperliquidAccount)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "position not found"})
		return
	}

	fills, err := s.liveStore.GetFills(r.Context(), id)
	if err != nil {
		s.logger.Error("live position: get fills", "err", err, "id", id)
	}
	if fills == nil {
		fills = []executor.LiveFill{}
	}

	events, err := s.liveStore.GetEvents(r.Context(), id)
	if err != nil {
		s.logger.Error("live position: get events", "err", err, "id", id)
	}
	if events == nil {
		events = []executor.LiveEvent{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"position": pos,
		"fills":    fills,
		"events":   events,
	})
}

func liveAccountsFromQuery(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	pacificaAccount := strings.TrimSpace(r.URL.Query().Get("account_pacifica"))
	hyperliquidAccount := strings.TrimSpace(r.URL.Query().Get("account_hyperliquid"))
	if pacificaAccount == "" || hyperliquidAccount == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "account_pacifica and account_hyperliquid required",
		})
		return "", "", false
	}
	return pacificaAccount, hyperliquidAccount, true
}

// handleLiveClose prepares close signing requests for a single live position.
//
// POST /api/v1/live/close/{id}
//
// Input: { "account_pacifica": "...", "account_hyperliquid": "..." }
//
// Returns signing requests for each filled leg. Frontend signs + submits
// each via /api/v1/live/submit (close/reduce-only actions are allowed).
func (s *Server) handleLiveClose(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "live execution not configured",
		})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/live/close/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "position id required"})
		return
	}

	var req struct {
		AccountPacifica    string `json:"account_pacifica"`
		AccountHyperliquid string `json:"account_hyperliquid"`
		AgentPacifica      string `json:"agent_pacifica"`
		AgentHyperliquid   string `json:"agent_hyperliquid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.AccountPacifica == "" || req.AccountHyperliquid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "account_pacifica and account_hyperliquid required",
		})
		return
	}
	if req.AgentPacifica == "" || req.AgentHyperliquid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_pacifica and agent_hyperliquid required"})
		return
	}
	authorized, err := s.live.agentPairAuthorizationMatches(
		r.Context(), req.AccountPacifica, req.AccountHyperliquid, req.AgentPacifica, req.AgentHyperliquid,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify trading agent authorization"})
		return
	}
	if !authorized {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "trading agent authorization not registered; reauthorize both agents"})
		return
	}

	pos, err := s.liveStore.GetPositionForAccounts(
		r.Context(), id, req.AccountPacifica, req.AccountHyperliquid,
	)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "position not found"})
		return
	}

	if pos.State != string(executor.ExecStateOpen) && pos.State != string(executor.ExecStateDegraded) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("position is %s, not closeable", pos.State),
		})
		return
	}

	reconciledClosed, err := s.reconcilePositionAbsentFromVenues(
		r.Context(), pos, req.AccountPacifica, req.AccountHyperliquid,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to reconcile venue exposure"})
		return
	}
	if reconciledClosed {
		writeJSON(w, http.StatusOK, map[string]any{
			"position_id":       id,
			"reconciled_closed": true,
			"signing_requests":  []any{},
		})
		return
	}

	fills, err := s.liveStore.GetFills(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get fills"})
		return
	}
	confirmedLegs, err := s.liveStore.ConfirmedCloseLegs(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get close progress"})
		return
	}

	var signingRequests []*domain.SigningRequest
	for _, fill := range fills {
		if !fill.Filled || fill.FilledAmount <= 0 || confirmedLegs[fill.Leg] {
			continue
		}
		cloid := fmt.Sprintf("close-%s-leg%d-%d", id[:8], fill.Leg, time.Now().UnixNano())
		sigReq, err := s.buildCloseSigningRequest(
			fill, cloid, req.AccountPacifica, req.AccountHyperliquid,
			req.AgentPacifica, req.AgentHyperliquid,
		)
		if err != nil {
			s.logger.Error("live close: build close payload", "err", err, "id", id, "leg", fill.Leg)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("leg %d close payload failed: %s", fill.Leg, err),
			})
			return
		}
		sigReq.PositionID = id
		sigReq.Leg = fill.Leg
		s.live.signingStore.Store(sigReq)
		signingRequests = append(signingRequests, sigReq)
	}

	if len(signingRequests) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no filled legs to close"})
		return
	}

	s.liveStore.InsertEvent(r.Context(), id, "close_prepared",
		executor.ExecState(pos.State),
		fmt.Sprintf("%d close orders prepared", len(signingRequests)))

	s.logger.Info("live close: signing requests ready",
		"id", id, "asset", pos.Asset, "legs", len(signingRequests))

	writeJSON(w, http.StatusOK, map[string]any{
		"position_id":      id,
		"signing_requests": signingRequests,
	})
}

func (s *Server) reconcilePositionAbsentFromVenues(
	ctx context.Context,
	position *executor.LivePosition,
	accountPacifica, accountHyperliquid string,
) (bool, error) {
	if s.live == nil || s.live.accounts == nil {
		return false, nil
	}
	updatedAt, err := time.Parse(time.RFC3339, position.UpdatedAt)
	if err != nil || time.Since(updatedAt) < positionReconciliationQuietPeriod {
		return false, nil
	}

	accounts, err := s.live.acquireAccounts(accountPacifica, accountHyperliquid)
	if err != nil {
		s.logger.Warn("live position: venue reconciliation unavailable", "err", err, "id", position.ID)
		return false, nil
	}
	defer accounts.Release()
	unlock := accounts.Lock()
	defer unlock()

	for _, venue := range []string{"pacifica", "hyperliquid"} {
		feed, ok := accounts.Feed(venue)
		if !ok || !positionStateReady(feed.Snapshot().PositionsUpdatedAt, updatedAt) {
			return false, nil
		}
		size, _ := currentVenuePosition(accounts, venue, position.Asset)
		if math.Abs(size) > 1e-9 {
			return false, nil
		}
	}

	changed, err := s.liveStore.MarkClosed(ctx, position.ID)
	if err != nil {
		return false, err
	}
	if changed {
		s.liveStore.InsertEvent(ctx, position.ID, "venue_reconciled_closed", executor.ExecStateClosed,
			"fresh venue state shows no remaining position on either venue")
		s.logger.Info("live position: reconciled closed from venue state", "id", position.ID, "asset", position.Asset)
	}
	return changed, nil
}

// handleLiveKill is the emergency kill switch — prepares close orders for all open live positions.
//
// POST /api/v1/live/kill
//
// Input:
//
//	{
//	  "account_pacifica": "...",
//	  "account_hyperliquid": "..."
//	}
//
// Flow:
//  1. Find all open/degraded positions
//  2. For each position, get fills to know what legs to close
//  3. Build close signing requests for each filled leg
//  4. Store signing requests; accepted submissions mark positions as "closing"
//  5. Return all signing requests — frontend signs + submits each via /api/v1/live/submit
//
// Idempotent — repeated calls regenerate signing requests for positions still open.
func (s *Server) handleLiveKill(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.liveStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "live execution not configured",
		})
		return
	}

	var req struct {
		AccountPacifica    string `json:"account_pacifica"`
		AccountHyperliquid string `json:"account_hyperliquid"`
		AgentPacifica      string `json:"agent_pacifica"`
		AgentHyperliquid   string `json:"agent_hyperliquid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.AccountPacifica == "" || req.AccountHyperliquid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "account_pacifica and account_hyperliquid required",
		})
		return
	}
	if req.AgentPacifica == "" || req.AgentHyperliquid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_pacifica and agent_hyperliquid required"})
		return
	}
	authorized, err := s.live.agentPairAuthorizationMatches(
		r.Context(), req.AccountPacifica, req.AccountHyperliquid, req.AgentPacifica, req.AgentHyperliquid,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify trading agent authorization"})
		return
	}
	if !authorized {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "trading agent authorization not registered; reauthorize both agents"})
		return
	}

	s.logger.Warn("kill switch: activated")

	ctx := r.Context()
	positions, err := s.live.liveStore.ListOpenPositionsForAccounts(
		ctx, req.AccountPacifica, req.AccountHyperliquid,
	)
	if err != nil {
		s.logger.Error("kill switch: list positions", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to list open positions",
		})
		return
	}

	if len(positions) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"targeted":         0,
			"signing_requests": []any{},
			"positions":        []any{},
		})
		return
	}

	type positionClose struct {
		ID       string `json:"id"`
		Asset    string `json:"asset"`
		State    string `json:"state"`
		Legs     int    `json:"legs_to_close"`
		Exposure []struct {
			Leg    int     `json:"leg"`
			Venue  string  `json:"venue"`
			Symbol string  `json:"symbol"`
			Side   string  `json:"side"`
			Amount float64 `json:"amount"`
		} `json:"remaining_exposure"`
		Error string `json:"error,omitempty"`
	}

	var signingRequests []*domain.SigningRequest
	var posResults []positionClose

	for _, pos := range positions {
		pc := positionClose{
			ID:    pos.ID,
			Asset: pos.Asset,
			State: pos.State,
		}

		fills, err := s.live.liveStore.GetFills(ctx, pos.ID)
		if err != nil {
			s.logger.Error("kill switch: get fills", "err", err, "id", pos.ID)
			pc.Error = "failed to get fills"
			posResults = append(posResults, pc)
			continue
		}
		confirmedLegs, err := s.live.liveStore.ConfirmedCloseLegs(ctx, pos.ID)
		if err != nil {
			s.logger.Error("kill switch: get close progress", "err", err, "id", pos.ID)
			pc.Error = "failed to get close progress"
			posResults = append(posResults, pc)
			continue
		}

		legsClosed := 0
		for _, fill := range fills {
			if !fill.Filled || fill.FilledAmount <= 0 || confirmedLegs[fill.Leg] {
				continue
			}
			pc.Exposure = append(pc.Exposure, struct {
				Leg    int     `json:"leg"`
				Venue  string  `json:"venue"`
				Symbol string  `json:"symbol"`
				Side   string  `json:"side"`
				Amount float64 `json:"amount"`
			}{fill.Leg, fill.Venue, fill.Symbol, fill.Side, fill.FilledAmount})

			cloid := fmt.Sprintf("kill-%s-leg%d-%d", pos.ID[:8], fill.Leg, time.Now().UnixNano())

			sigReq, err := s.buildCloseSigningRequest(
				fill,
				cloid,
				req.AccountPacifica,
				req.AccountHyperliquid,
				req.AgentPacifica,
				req.AgentHyperliquid,
			)
			if err != nil {
				s.logger.Error("kill switch: build close payload",
					"err", err, "id", pos.ID, "leg", fill.Leg, "venue", fill.Venue)
				pc.Error = fmt.Sprintf("leg %d: %s", fill.Leg, err)
				continue
			}
			sigReq.PositionID = pos.ID
			sigReq.Leg = fill.Leg

			s.live.signingStore.Store(sigReq)
			signingRequests = append(signingRequests, sigReq)
			legsClosed++

			s.logger.Info("kill switch: close payload ready",
				"position", pos.ID,
				"leg", fill.Leg,
				"venue", fill.Venue,
				"symbol", fill.Symbol,
				"amount", fill.FilledAmount,
			)
		}

		pc.Legs = legsClosed

		if legsClosed > 0 {
			s.live.liveStore.InsertEvent(ctx, pos.ID, "emergency_close_prepared",
				executor.ExecState(pos.State),
				fmt.Sprintf("kill switch: %d close orders prepared", legsClosed))
		}

		posResults = append(posResults, pc)
	}

	s.logger.Warn("kill switch: close payloads ready",
		"positions", len(positions),
		"signing_requests", len(signingRequests),
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"targeted":         len(positions),
		"signing_requests": signingRequests,
		"positions":        posResults,
	})
}

// buildCloseSigningRequest builds a close signing request for a single filled leg.
// accountPacifica is only used for Pacifica; Hyperliquid derives the account from
// the signature at submit time.
func (s *Server) buildCloseSigningRequest(
	fill executor.LiveFill,
	clientOrderID string,
	accountPacifica, accountHyperliquid, agentPacifica, agentHyperliquid string,
) (*domain.SigningRequest, error) {
	positionSide := domain.Side(fill.Side)
	price := fill.AvgFillPrice // use fill price as reference for slippage calc

	var request *domain.SigningRequest
	var err error
	switch fill.Venue {
	case "pacifica":
		request, err = paclive.BuildClosePayload(
			accountPacifica,
			fill.Symbol,
			positionSide,
			fill.FilledAmount,
			price,
			clientOrderID,
		)
	case "hyperliquid":
		if s.live.hlAssetMap == nil {
			return nil, fmt.Errorf("hyperliquid asset map not configured")
		}
		request, err = hllive.BuildClosePayload(
			s.live.hlAssetMap,
			fill.Symbol,
			positionSide,
			fill.FilledAmount,
			price,
			clientOrderID,
		)
	default:
		return nil, fmt.Errorf("unsupported venue: %s", fill.Venue)
	}
	if err == nil {
		request.Account = accountForVenue(fill.Venue, accountPacifica, accountHyperliquid)
		request.Signer = signerForVenue(fill.Venue, agentPacifica, agentHyperliquid)
		if request.Signer == "" {
			return nil, fmt.Errorf("%s trading agent required", fill.Venue)
		}
	}
	return request, err
}
