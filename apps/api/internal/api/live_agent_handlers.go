package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacificlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

const maxAgentAuthorizationBody = 16 << 10

type asterAgentApprover interface {
	ApproveAgent(context.Context, asterlive.ApproveAgentRequest) error
}

type hyperliquidAgentApprover interface {
	ApproveAgent(context.Context, hllive.ApproveAgentRequest) error
}

type hyperliquidBuilderApprover interface {
	ApproveBuilderFee(context.Context, hllive.ApproveBuilderFeeRequest) error
}

type pacificaAgentBinder interface {
	BindAgent(context.Context, pacificlive.BindAgentRequest) error
}

type pacificaAgentRevoker interface {
	RevokeAgent(context.Context, pacificlive.RevokeAgentRequest) error
}

type pacificaBuilderCodeApprover interface {
	ApproveBuilderCode(context.Context, pacificlive.ApproveBuilderCodeRequest) error
}

type pacificaBuilderCodeApprovalReader interface {
	HasBuilderCodeApproval(context.Context, string, pacificlive.BuilderConfig) (bool, error)
}

func (s *Server) handleAsterAgentApprove(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.asterAgentApprover == nil || s.live.asterBuilder == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live agent authorization unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request asterlive.ApproveAgentRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := request.Validate(time.Now(), *s.live.asterBuilder); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	unlockOwner, err := s.live.lockAgentOwner("aster", request.User)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Aster owner is busy; retry authorization"})
		return
	}
	defer unlockOwner()
	blocked, err := s.agentChangeBlocked(r.Context(), "aster", request.User)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to inspect active live sessions"})
		return
	}
	if blocked {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot reauthorize agent during an active live session"})
		return
	}
	if err := s.live.asterAgentApprover.ApproveAgent(r.Context(), request); err != nil {
		switch {
		case errors.Is(err, asterlive.ErrSubmissionNotSent):
			s.logger.Warn("Aster agent approval was not sent", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"status": "not_sent", "error": "Aster agent approval was not sent"})
			return
		case errors.Is(err, asterlive.ErrApprovalRejected):
			s.logger.Warn("Aster agent approval rejected", "err", err)
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"status": "rejected", "error": "Aster agent approval rejected"})
			return
		default:
			s.logger.Warn("Aster agent approval outcome uncertain", "err", err)
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "uncertain"})
			return
		}
	}
	if err := s.live.recordAgentAuthorization(r.Context(), "aster", request.User, request.AgentAddress); err != nil {
		s.logger.Warn("Aster agent approved but local registration is uncertain", "err", err)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "uncertain"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAsterAgentReconcile(w http.ResponseWriter, r *http.Request) {
	if !s.asterDataAgentAvailable(w) || s.live == nil {
		return
	}
	var request struct {
		Account    string   `json:"account"`
		Candidates []string `json:"candidates"`
	}
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	if len(request.Candidates) == 0 || len(request.Candidates) > 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "one or two Aster agent candidates required"})
		return
	}
	unlockOwner, err := s.live.lockAgentOwner("aster", request.Account)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Aster owner is busy; retry verification"})
		return
	}
	defer unlockOwner()
	agent, err := s.asterDataAgent.ReconcileExecutionAgent(r.Context(), request.Account, request.Candidates)
	if err != nil {
		writeAsterDataAgentError(w, err)
		return
	}
	if err := s.live.recordAgentAuthorization(r.Context(), "aster", request.Account, agent); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Aster agent verified but local registration failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"agent_address": agent})
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
	unlockOwner, err := s.live.lockAgentOwner("hyperliquid", request.OwnerAddress)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Hyperliquid owner is busy; retry authorization"})
		return
	}
	defer unlockOwner()
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

func (s *Server) handleHyperliquidBuilderFeeApprove(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.hlBuilder == nil || s.live.hlBuilderApprover == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Hyperliquid builder fee unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request hllive.ApproveBuilderFeeRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := request.Validate(time.Now(), s.live.hlBuilder.Address); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.live.hlBuilderApprover.ApproveBuilderFee(r.Context(), request); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Hyperliquid builder fee approval rejected"})
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
	unlockOwner, err := s.live.lockAgentOwner("pacifica", request.Account)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Pacifica owner is busy; retry authorization"})
		return
	}
	defer unlockOwner()
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

func (s *Server) handlePacificaAgentRevoke(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.pacificaAgentRevoker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live agent revocation unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request pacificlive.RevokeAgentRequest
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
	unlockOwner, err := s.live.lockAgentOwner("pacifica", request.Account)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Pacifica owner is busy; retry revocation"})
		return
	}
	defer unlockOwner()
	blocked, err := s.agentChangeBlocked(r.Context(), "pacifica", request.Account)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to inspect active live sessions"})
		return
	}
	if blocked {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot revoke agent during an active live session"})
		return
	}
	if err := s.live.pacificaAgentRevoker.RevokeAgent(r.Context(), request); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Pacifica agent revocation rejected"})
		return
	}
	if err := s.live.removeAgentAuthorization(r.Context(), "pacifica", request.Account, request.AgentWallet); err != nil {
		if s.logger != nil {
			s.logger.Warn("Pacifica agent revoked but local registration cleanup failed", "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePacificaBuilderCodeApprove(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.pacificaBuilder == nil || s.live.pacificaBuilderApprover == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Pacifica builder approval unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request pacificlive.ApproveBuilderCodeRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := request.Validate(time.Now(), *s.live.pacificaBuilder); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.live.pacificaBuilderApprover.ApproveBuilderCode(r.Context(), request); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Pacifica builder approval rejected"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePacificaBuilderCodeApproval(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.pacificaBuilder == nil || s.live.pacificaBuilderApprovalReader == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Pacifica builder approval unavailable"})
		return
	}
	approved, err := s.live.pacificaBuilderApprovalReader.HasBuilderCodeApproval(
		r.Context(), r.URL.Query().Get("account"), *s.live.pacificaBuilder,
	)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to verify Pacifica builder approval"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"approved": approved})
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
		account := durableRecordAccountBindings(record)[venue]
		if account != "" && sameVenueBinding(venue, account, owner) {
			return true, nil
		}
	}
	return false, nil
}
