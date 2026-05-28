package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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

	var req struct {
		OpportunityID      string  `json:"opportunity_id"`
		Leverage           float64 `json:"leverage"`
		AccountPacifica    string  `json:"account_pacifica"`
		AccountHyperliquid string  `json:"account_hyperliquid"`
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

	// 1. Build a fresh execution plan
	plan, err := s.scanner.BuildPlan(r.Context(), req.OpportunityID, req.Leverage)
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

	// 3. Live admission gate
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

	// 4. Build signing requests for each leg
	now := time.Now()
	leg1ID := fmt.Sprintf("orbital-leg1-%d", now.UnixNano())
	leg2ID := fmt.Sprintf("orbital-leg2-%d", now.UnixNano()+1)

	sigReq1, err := s.buildLegSigningRequest(
		plan.Leg1, plan.Notional, leg1ID,
		req.AccountPacifica, req.AccountHyperliquid,
	)
	if err != nil {
		s.logger.Error("live prepare: build leg1 signing request", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("leg1 payload build failed: %s", err),
		})
		return
	}

	sigReq2, err := s.buildLegSigningRequest(
		plan.Leg2, plan.Notional, leg2ID,
		req.AccountPacifica, req.AccountHyperliquid,
	)
	if err != nil {
		s.logger.Error("live prepare: build leg2 signing request", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("leg2 payload build failed: %s", err),
		})
		return
	}

	// 5. Store signing requests
	s.live.signingStore.Store(sigReq1)
	s.live.signingStore.Store(sigReq2)

	s.logger.Info("live prepare: signing requests ready",
		"asset", opp.Asset,
		"leg1_venue", sigReq1.Venue,
		"leg2_venue", sigReq2.Venue,
		"plan_id", plan.ID,
	)

	// 6. Return both signing requests + plan context
	writeJSON(w, http.StatusOK, map[string]any{
		"plan_id":          plan.ID,
		"asset":            plan.Asset,
		"notional":         plan.Notional,
		"leverage":         plan.Leverage,
		"signing_requests": []*domain.SigningRequest{sigReq1, sigReq2},
	})
}

func (s *Server) buildLegSigningRequest(
	leg domain.Leg,
	notional float64,
	clientOrderID string,
	accountPacifica string,
	accountHyperliquid string,
) (*domain.SigningRequest, error) {
	switch leg.Venue {
	case "pacifica":
		return paclive.BuildOpenPayload(
			accountPacifica,
			leg.Asset,
			leg.Side,
			notional,
			leg.ExpectedPrice,
			clientOrderID,
		)
	case "hyperliquid":
		if s.live.hlAssetMap == nil {
			return nil, fmt.Errorf("hyperliquid asset map not configured")
		}
		return hllive.BuildOpenPayload(
			s.live.hlAssetMap,
			leg.Asset,
			leg.Side,
			notional,
			leg.ExpectedPrice,
			clientOrderID,
		)
	default:
		return nil, fmt.Errorf("unsupported venue: %s", leg.Venue)
	}
}

// handleLiveSubmit accepts a user-signed venue action and submits it.
//
// POST /api/v1/live/submit
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

	// 2. Submit through venue-specific path
	var result *domain.SubmissionResult

	switch signed.Venue {
	case "pacifica":
		if s.live.pacClient == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "pacifica live client not configured",
			})
			return
		}
		result, err = s.live.pacClient.SubmitSignedOrder(
			r.Context(), signed, sigReq, s.live.pacTracker,
		)

	case "hyperliquid":
		if s.live.hlClient == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "hyperliquid live client not configured",
			})
			return
		}
		result, err = s.live.hlClient.SubmitSignedOrder(
			r.Context(), signed, sigReq,
		)

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("unsupported venue: %s", signed.Venue),
		})
		return
	}

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

// handleLivePositions returns all live positions, newest first.
//
// GET /api/v1/live/positions
func (s *Server) handleLivePositions(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.liveStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "live execution not configured",
		})
		return
	}

	positions, err := s.live.liveStore.ListPositions(r.Context())
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
	if s.live == nil || s.live.liveStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "live execution not configured",
		})
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/live/positions/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}

	pos, err := s.live.liveStore.GetPosition(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "position not found"})
		return
	}

	fills, err := s.live.liveStore.GetFills(r.Context(), id)
	if err != nil {
		s.logger.Error("live position: get fills", "err", err, "id", id)
	}
	if fills == nil {
		fills = []executor.LiveFill{}
	}

	events, err := s.live.liveStore.GetEvents(r.Context(), id)
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
				req.AccountHyperliquid,
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
func (s *Server) buildCloseSigningRequest(
	fill executor.LiveFill,
	clientOrderID string,
	accountPacifica string,
	accountHyperliquid string,
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
