package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/paper"
)

func (s *Server) handlePaperOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OpportunityID     string   `json:"opportunity_id"`
		Leverage          float64  `json:"leverage"` // shared fallback
		LeverageLong      *float64 `json:"leverage_long,omitempty"`
		LeverageShort     *float64 `json:"leverage_short,omitempty"`
		RequestedNotional *float64 `json:"requested_notional,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.OpportunityID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "opportunity_id required"})
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
	levLong, levShort, err := resolveLegLeverage(req.Leverage, req.LeverageLong, req.LeverageShort)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	plan, err := s.scanner.BuildPlan(r.Context(), req.OpportunityID, levLong, levShort, notional)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if !plan.Executable {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "plan not executable"})
		return
	}

	go func() {
		s.executor.Execute(s.ctx, plan)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "executing", "plan_id": plan.ID})
}

func (s *Server) handlePaperPositions(w http.ResponseWriter, r *http.Request) {
	positions := s.store.List()
	writeJSON(w, http.StatusOK, positions)
}

func (s *Server) handlePaperPosition(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/paper/positions/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}

	pos := s.store.Get(id)
	if pos == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "position not found"})
		return
	}

	writeJSON(w, http.StatusOK, pos)
}

func (s *Server) handlePaperClose(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/paper/close/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}

	if err := s.executor.CloseByID(s.ctx, id, paper.CloseReasonManual); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, s.store.Get(id))
}
