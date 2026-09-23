package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
)

const maxAsterDataAgentBody = 16 << 10

type AsterDataAgentProbe interface {
	Prepare(context.Context, string, string) (dataagent.Prepared, error)
	Authorize(context.Context, string, string, string, string, dataagent.Approval) error
	Validate(context.Context, string, string, string, string) (dataagent.Report, error)
	Status(context.Context, string, string) (dataagent.ProbeStatus, error)
	Run(context.Context, string, string) (dataagent.Report, error)
	ReconcileExecutionAgent(context.Context, string, []string) (string, error)
}

func (s *Server) handleAsterDataAgentAuthorize(w http.ResponseWriter, r *http.Request) {
	if !s.asterDataAgentAvailable(w) {
		return
	}
	var request struct {
		ProbeID        string             `json:"probe_id"`
		Signature      string             `json:"signature"`
		Account        string             `json:"account"`
		ExecutionAgent string             `json:"execution_agent"`
		Approval       dataagent.Approval `json:"approval"`
	}
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	unlock, ok := s.lockAsterDataAgentOwner(w, r, request.Account)
	if !ok {
		return
	}
	defer unlock()
	if err := s.asterDataAgent.Authorize(
		r.Context(), request.ProbeID, request.Signature, request.Account,
		request.ExecutionAgent, request.Approval,
	); err != nil {
		writeAsterDataAgentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) EnableAsterDataAgentProbe(probe AsterDataAgentProbe) {
	s.asterDataAgent = probe
}

func (s *Server) handleAsterDataAgentPrepare(w http.ResponseWriter, r *http.Request) {
	if !s.asterDataAgentAvailable(w) {
		return
	}
	var request struct {
		Account        string `json:"account"`
		ExecutionAgent string `json:"execution_agent"`
	}
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	unlock, ok := s.lockAsterDataAgentOwner(w, r, request.Account)
	if !ok {
		return
	}
	defer unlock()
	prepared, err := s.asterDataAgent.Prepare(r.Context(), request.Account, request.ExecutionAgent)
	if err != nil {
		writeAsterDataAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prepared)
}

func (s *Server) handleAsterDataAgentValidate(w http.ResponseWriter, r *http.Request) {
	if !s.asterDataAgentAvailable(w) {
		return
	}
	var request struct {
		ProbeID        string `json:"probe_id"`
		Signature      string `json:"signature"`
		Account        string `json:"account"`
		ExecutionAgent string `json:"execution_agent"`
	}
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	unlock, ok := s.lockAsterDataAgentOwner(w, r, request.Account)
	if !ok {
		return
	}
	defer unlock()
	report, err := s.asterDataAgent.Validate(
		r.Context(), request.ProbeID, request.Signature, request.Account, request.ExecutionAgent,
	)
	if err != nil {
		writeAsterDataAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleAsterDataAgentStatus(w http.ResponseWriter, r *http.Request) {
	if !s.asterDataAgentAvailable(w) {
		return
	}
	status, err := s.asterDataAgent.Status(
		r.Context(), r.URL.Query().Get("account"), r.URL.Query().Get("execution_agent"),
	)
	if err != nil {
		writeAsterDataAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleAsterDataAgentRun(w http.ResponseWriter, r *http.Request) {
	if !s.asterDataAgentAvailable(w) {
		return
	}
	var request struct {
		Account        string `json:"account"`
		ExecutionAgent string `json:"execution_agent"`
	}
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	report, err := s.asterDataAgent.Run(r.Context(), request.Account, request.ExecutionAgent)
	if err != nil {
		writeAsterDataAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) asterDataAgentAvailable(w http.ResponseWriter) bool {
	if s.asterDataAgent != nil {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "Aster data-agent probe unavailable: ASTER_DATA_AGENT_MASTER_KEY must be standard base64 encoding of exactly 32 bytes",
	})
	return false
}

func (s *Server) lockAsterDataAgentOwner(w http.ResponseWriter, r *http.Request, owner string) (func(), bool) {
	if s.live == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live agent authorization unavailable"})
		return nil, false
	}
	unlock, err := s.live.lockAgentOwner("aster", owner)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Aster owner is busy; retry probe"})
		return nil, false
	}
	blocked, err := s.agentChangeBlocked(r.Context(), "aster", owner)
	if err != nil {
		unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to inspect active live sessions"})
		return nil, false
	}
	if blocked {
		unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot authorize data agent during an active live session"})
		return nil, false
	}
	return unlock, true
}

func writeAsterDataAgentError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	message := "Aster data-agent request failed"
	switch {
	case errors.Is(err, dataagent.ErrInvalidInput):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, dataagent.ErrNotFound):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, dataagent.ErrRejected), errors.Is(err, dataagent.ErrNotSent), errors.Is(err, dataagent.ErrStale):
		status, message = http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, dataagent.ErrConflict), errors.Is(err, dataagent.ErrUncertain),
		errors.Is(err, dataagent.ErrNotApproved), errors.Is(err, dataagent.ErrExecutionAgentMismatch):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, dataagent.ErrUnavailable):
		status, message = http.StatusServiceUnavailable, err.Error()
	case errors.Is(err, dataagent.ErrCredentialUnreadable):
		status, message = http.StatusServiceUnavailable, err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAsterDataAgentBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	return true
}
