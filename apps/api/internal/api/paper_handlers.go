package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/paper"
)

func (s *Server) handlePaperOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OpportunityID string `json:"opportunity_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.OpportunityID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "opportunity_id required"})
		return
	}

	plan, err := s.scanner.BuildPlan(r.Context(), req.OpportunityID)
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
