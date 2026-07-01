package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
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
	s.live.sessions.cleanup() // evict stale sessions opportunistically

	var req struct {
		OpportunityID      string   `json:"opportunity_id"`
		Leverage           float64  `json:"leverage"`
		RequestedNotional  *float64 `json:"requested_notional,omitempty"`
		AccountPacifica    string   `json:"account_pacifica"`
		AccountHyperliquid string   `json:"account_hyperliquid"`
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
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
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

	// 3. Start account subscribers if not already running (lazy — first prepare triggers them)
	s.live.EnsureAccountStreams(req.AccountPacifica, req.AccountHyperliquid)

	// 4. Live admission gate
	admission := domain.CheckLiveAdmission(*opp, plan.Leverage.Leverage)
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

	// 4. Riskier leg first (higher slippage = thinner book → submit first).
	leg1, leg2 := orderLegsByRisk(plan)

	// 5. Build leg-1 OPEN + leg-1 reduce-only UNWIND signing requests.
	// Both are on the riskier leg's venue/wallet and signed together up front,
	// so the backend holds a signature-free escape the moment leg 1 fills.
	now := time.Now()
	leg1OpenCloid := fmt.Sprintf("orbital-l1open-%d", now.UnixNano())
	leg1UnwindCloid := fmt.Sprintf("orbital-l1unwind-%d", now.UnixNano()+1)

	leg1Open, err := s.buildOpenSigningRequest(
		leg1, plan.Notional, leg1OpenCloid, req.AccountPacifica,
	)
	if err != nil {
		s.logger.Error("live prepare: build leg1 open", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("leg1 open payload build failed: %s", err),
		})
		return
	}

	leg1Unwind, err := s.buildUnwindSigningRequest(
		leg1, plan.Notional, leg1UnwindCloid, req.AccountPacifica,
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
		State:              sessAwaitingLeg1Signs,
		Leg1OpenReqID:      leg1Open.ID,
		Leg1UnwindReqID:    leg1Unwind.ID,
		CreatedAt:          now,
	}
	s.live.sessions.put(sess)

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
	leg legPlan, amount float64, clientOrderID, accountPacifica string,
) (*domain.SigningRequest, error) {
	switch leg.venue {
	case "pacifica":
		return paclive.BuildOpenPayload(accountPacifica, leg.symbol, leg.side, amount, leg.price, clientOrderID)
	case "hyperliquid":
		if s.live.hlAssetMap == nil {
			return nil, fmt.Errorf("hyperliquid asset map not configured")
		}
		return hllive.BuildOpenPayload(s.live.hlAssetMap, leg.symbol, leg.side, amount, leg.price, clientOrderID)
	default:
		return nil, fmt.Errorf("unsupported venue: %s", leg.venue)
	}
}

// buildUnwindSigningRequest builds a reduce-only close signing request for one leg.
// Side is the position side; the close payload inverts it internally.
func (s *Server) buildUnwindSigningRequest(
	leg legPlan, amount float64, clientOrderID, accountPacifica string,
) (*domain.SigningRequest, error) {
	switch leg.venue {
	case "pacifica":
		return paclive.BuildClosePayload(accountPacifica, leg.symbol, leg.side, amount, leg.price, clientOrderID)
	case "hyperliquid":
		if s.live.hlAssetMap == nil {
			return nil, fmt.Errorf("hyperliquid asset map not configured")
		}
		return hllive.BuildClosePayload(s.live.hlAssetMap, leg.symbol, leg.side, amount, leg.price, clientOrderID)
	default:
		return nil, fmt.Errorf("unsupported venue: %s", leg.venue)
	}
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
	if sigReq.Action == "open" || (!sigReq.ReduceOnly && sigReq.Action != "close") {
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
		s.logger.Error("live submit: submission error",
			"request_id", signed.RequestID,
			"venue", signed.Venue,
			"err", err,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("submission failed: %s", err),
		})
		return
	}

	if result == nil {
		s.logger.Error("live submit: nil result without error",
			"request_id", signed.RequestID,
			"venue", signed.Venue,
		)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "submission returned no result",
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

	writeJSON(w, http.StatusOK, result)
}

// handleLiveBalances returns current available balance per venue from account state.
//
// GET /api/v1/live/balances
//
// Returns {"pacifica": {...}, "hyperliquid": {...}} with equity, available, connected.
// Returns zeros/disconnected if account streams haven't started yet (wallets not connected).
func (s *Server) handleLiveBalances(w http.ResponseWriter, _ *http.Request) {
	type venueBalance struct {
		Venue     string  `json:"venue"`
		Equity    float64 `json:"equity"`
		Available float64 `json:"available"`
		Connected bool    `json:"connected"`
	}

	var pac, hl venueBalance
	pac.Venue = "pacifica"
	hl.Venue = "hyperliquid"

	if s.live != nil && s.live.pacState != nil {
		snap := s.live.pacState.Snapshot()
		pac.Equity = snap.Equity
		pac.Available = snap.AvailableToSpend
		pac.Connected = snap.Connected
	}
	if s.live != nil && s.live.hlState != nil {
		snap := s.live.hlState.Snapshot()
		hl.Equity = snap.Margin.AccountEquity
		hl.Available = snap.Margin.AvailableBalance
		hl.Connected = snap.Connected
	}

	writeJSON(w, http.StatusOK, map[string]venueBalance{
		"pacifica":    pac,
		"hyperliquid": hl,
	})
}

// handleLivePositions returns all live positions, newest first.
//
// GET /api/v1/live/positions
func (s *Server) handleLivePositions(w http.ResponseWriter, r *http.Request) {
	positions, err := s.liveStore.ListPositions(r.Context())
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

	pos, err := s.liveStore.GetPosition(r.Context(), id)
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

	pos, err := s.liveStore.GetPosition(r.Context(), id)
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

	fills, err := s.liveStore.GetFills(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get fills"})
		return
	}

	var signingRequests []*domain.SigningRequest
	for _, fill := range fills {
		if !fill.Filled || fill.FilledAmount <= 0 {
			continue
		}
		cloid := fmt.Sprintf("close-%s-leg%d-%d", id[:8], fill.Leg, time.Now().UnixNano())
		sigReq, err := s.buildCloseSigningRequest(fill, cloid, req.AccountPacifica)
		if err != nil {
			s.logger.Error("live close: build close payload", "err", err, "id", id, "leg", fill.Leg)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("leg %d close payload failed: %s", fill.Leg, err),
			})
			return
		}
		s.live.signingStore.Store(sigReq)
		signingRequests = append(signingRequests, sigReq)
	}

	if len(signingRequests) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no filled legs to close"})
		return
	}

	s.liveStore.MarkClosing(r.Context(), id)
	s.liveStore.InsertEvent(r.Context(), id, "close_initiated",
		executor.ExecStateClosing,
		fmt.Sprintf("%d close orders prepared", len(signingRequests)))

	s.logger.Info("live close: signing requests ready",
		"id", id, "asset", pos.Asset, "legs", len(signingRequests))

	writeJSON(w, http.StatusOK, map[string]any{
		"position_id":      id,
		"signing_requests": signingRequests,
	})
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
//  4. Store signing requests, mark positions as "closing"
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

	s.logger.Warn("kill switch: activated")

	ctx := r.Context()
	positions, err := s.live.liveStore.ListOpenPositions(ctx)
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
		ID    string `json:"id"`
		Asset string `json:"asset"`
		State string `json:"state"`
		Legs  int    `json:"legs_to_close"`
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

		legsClosed := 0
		for _, fill := range fills {
			if !fill.Filled || fill.FilledAmount <= 0 {
				continue
			}

			cloid := fmt.Sprintf("kill-%s-leg%d-%d", pos.ID[:8], fill.Leg, time.Now().UnixNano())

			sigReq, err := s.buildCloseSigningRequest(
				fill,
				cloid,
				req.AccountPacifica,
			)
			if err != nil {
				s.logger.Error("kill switch: build close payload",
					"err", err, "id", pos.ID, "leg", fill.Leg, "venue", fill.Venue)
				pc.Error = fmt.Sprintf("leg %d: %s", fill.Leg, err)
				continue
			}

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

		// Mark position as closing
		if legsClosed > 0 {
			s.live.liveStore.MarkClosing(ctx, pos.ID)
			s.live.liveStore.InsertEvent(ctx, pos.ID, "emergency_close_initiated",
				executor.ExecStateClosing,
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
	accountPacifica string,
) (*domain.SigningRequest, error) {
	positionSide := domain.Side(fill.Side)
	price := fill.AvgFillPrice // use fill price as reference for slippage calc

	switch fill.Venue {
	case "pacifica":
		return paclive.BuildClosePayload(
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
		return hllive.BuildClosePayload(
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
}
