package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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
	blocked, err := s.agentChangeBlocked(r.Context(), "hyperliquid", request.OwnerAddress)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to inspect active live sessions"})
		return
	}
	if blocked {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot reauthorize agent during an active live session"})
		return
	}
	if err := s.live.hlAgentApprover.ApproveAgent(r.Context(), request); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Hyperliquid agent approval rejected"})
		return
	}
	if err := s.live.recordAgentAuthorization(r.Context(), "hyperliquid", request.OwnerAddress, request.Action.AgentAddress); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Hyperliquid agent approved but local registration failed; reauthorize"})
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
	blocked, err := s.agentChangeBlocked(r.Context(), "pacifica", request.Account)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to inspect active live sessions"})
		return
	}
	if blocked {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot reauthorize agent during an active live session"})
		return
	}
	if err := s.live.pacificaAgentBinder.BindAgent(r.Context(), request); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Pacifica agent binding rejected"})
		return
	}
	if err := s.live.recordAgentAuthorization(r.Context(), "pacifica", request.Account, request.AgentWallet); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Pacifica agent bound but local registration failed; reauthorize"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentChangeBlocked(ctx context.Context, venue, owner string) (bool, error) {
	if s.liveStore == nil {
		return false, nil
	}
	records, err := s.liveStore.ListActiveDurableSessions(ctx)
	if err != nil {
		return false, err
	}
	owner = strings.TrimSpace(owner)
	for _, record := range records {
		switch venue {
		case "pacifica":
			if strings.TrimSpace(record.AccountPacifica) == owner {
				return true, nil
			}
		case "hyperliquid":
			if strings.EqualFold(strings.TrimSpace(record.AccountHyperliquid), owner) {
				return true, nil
			}
		}
	}
	return false, nil
}
