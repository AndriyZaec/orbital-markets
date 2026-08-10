package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacificlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

const maxAgentAuthorizationBody = 16 << 10

type hyperliquidAgentApprover interface {
	ApproveAgent(context.Context, hllive.ApproveAgentRequest) error
}

type pacificaAgentBinder interface {
	BindAgent(context.Context, pacificlive.BindAgentRequest) error
}

func (s *Server) handleHyperliquidAgentApprove(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.hlAgentApprover == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live agent authorization unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request hllive.ApproveAgentRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := request.Validate(time.Now()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.live.hlAgentApprover.ApproveAgent(r.Context(), request); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePacificaAgentBind(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.pacificaAgentBinder == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live agent authorization unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request pacificlive.BindAgentRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := request.Validate(time.Now()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.live.pacificaAgentBinder.BindAgent(r.Context(), request); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
