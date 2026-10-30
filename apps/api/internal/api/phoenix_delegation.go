package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/delegation"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
)

type PhoenixDelegation interface {
	Prepare(context.Context, delegation.PrepareRequest) (delegation.Result, error)
	Submit(context.Context, string, domain.SignedAction) (delegation.Result, error)
	Reconcile(context.Context, string, string) (delegation.Result, error)
}

func (s *Server) EnablePhoenixDelegation(service PhoenixDelegation) { s.phoenixDelegation = service }

func (s *Server) phoenixOwnerChange(w http.ResponseWriter, r *http.Request, owner string) (func(), bool) {
	unlock := func() {}
	if s.live != nil {
		var err error
		unlock, err = s.live.lockAgentOwner("phoenix", owner)
		if err != nil {
			writeJSON(w, 503, map[string]string{"error": "Phoenix owner busy"})
			return nil, false
		}
	}
	blocked, err := s.agentChangeBlocked(r.Context(), "phoenix", owner)
	if err != nil || blocked {
		unlock()
		writeJSON(w, 409, map[string]string{"error": "Phoenix agent change unavailable during active session"})
		return nil, false
	}
	return unlock, true
}

func (s *Server) handlePhoenixDelegationPrepare(w http.ResponseWriter, r *http.Request) {
	if s.phoenixDelegation == nil {
		writeJSON(w, 503, map[string]string{"error": "Phoenix delegation unavailable"})
		return
	}
	var request delegation.PrepareRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	unlock, ok := s.phoenixOwnerChange(w, r, request.Owner)
	if !ok {
		return
	}
	defer unlock()
	result, err := s.phoenixDelegation.Prepare(r.Context(), request)
	s.writePhoenixDelegation(w, result, err)
}

func (s *Server) handlePhoenixDelegationSubmit(w http.ResponseWriter, r *http.Request) {
	if s.phoenixDelegation == nil {
		writeJSON(w, 503, map[string]string{"error": "Phoenix delegation unavailable"})
		return
	}
	var request struct {
		Owner        string              `json:"owner"`
		SignedAction domain.SignedAction `json:"signed_action"`
	}
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	unlock, ok := s.phoenixOwnerChange(w, r, request.Owner)
	if !ok {
		return
	}
	defer unlock()
	result, err := s.phoenixDelegation.Submit(r.Context(), request.Owner, request.SignedAction)
	s.writePhoenixDelegation(w, result, err)
}

func (s *Server) handlePhoenixDelegationReconcile(w http.ResponseWriter, r *http.Request) {
	if s.phoenixDelegation == nil {
		writeJSON(w, 503, map[string]string{"error": "Phoenix delegation unavailable"})
		return
	}
	var request struct {
		Owner     string `json:"owner"`
		RequestID string `json:"request_id"`
	}
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	result, err := s.phoenixDelegation.Reconcile(r.Context(), request.Owner, request.RequestID)
	s.writePhoenixDelegation(w, result, err)
}

func (s *Server) writePhoenixDelegation(w http.ResponseWriter, result delegation.Result, err error) {
	if err != nil {
		status, message := http.StatusServiceUnavailable, "Phoenix delegation could not be verified"
		switch {
		case errors.Is(err, delegation.ErrInvalid), errors.Is(err, relayer.ErrInvalidSignature), errors.Is(err, relayer.ErrSignerMismatch):
			status, message = 400, "invalid Phoenix owner authorization"
		case errors.Is(err, delegation.ErrNotFound):
			status, message = 404, err.Error()
		case errors.Is(err, delegation.ErrBusy), errors.Is(err, delegation.ErrReplacementRequired), errors.Is(err, delegation.ErrAuthorityChanged), errors.Is(err, rpc.ErrExpired):
			status, message = 409, err.Error()
		case errors.Is(err, relayer.ErrFeeCap), errors.Is(err, relayer.ErrInsufficientBalance), errors.Is(err, rpc.ErrAccountNotFound):
			status, message = 422, err.Error()
		}
		if s.logger != nil {
			s.logger.Warn("Phoenix delegation request failed", "err", err)
		}
		writeJSON(w, status, struct {
			Error  string            `json:"error"`
			Result delegation.Result `json:"result"`
		}{message, result})
		return
	}
	status := 200
	switch result.State {
	case "pending":
		status = 202
	case "failed":
		status = 422
	case "expired", "superseded":
		status = 409
	}
	writeJSON(w, status, result)
}
