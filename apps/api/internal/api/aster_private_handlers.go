package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
)

type asterPrivateSubmitter interface {
	SubmitSignedPrivate(context.Context, domain.SignedAction, *domain.SigningRequest) (*asterlive.PrivateResult, error)
}

func (s *Server) handleAsterAccountPrepare(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.signingStore == nil || s.live.asterPrivate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Aster account snapshots unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Account string `json:"account"`
		Agent   string `json:"agent"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	payloads, err := asterlive.BuildAccountSnapshotPayloads(input.Account, input.Agent)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	unlockOwner, err := s.live.lockAgentOwner("aster", input.Account)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Aster owner is busy; retry request"})
		return
	}
	defer unlockOwner()
	authorized, err := s.live.agentAuthorizationMatches(r.Context(), "aster", input.Account, input.Agent)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify Aster authorization"})
		return
	}
	if !authorized {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Aster agent is not authorized for this owner"})
		return
	}
	for _, request := range payloads.Requests {
		s.live.signingStore.Store(request)
	}
	writeJSON(w, http.StatusOK, payloads)
}

func (s *Server) handleAsterPrivatePrepare(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.signingStore == nil || s.live.asterPrivate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Aster private requests unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Operation     string `json:"operation"`
		Account       string `json:"account"`
		Agent         string `json:"agent"`
		Symbol        string `json:"symbol,omitempty"`
		ClientOrderID string `json:"client_order_id,omitempty"`
		Leverage      int    `json:"leverage,omitempty"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	operation, ok := asterlive.ParsePrivateOperation(input.Operation)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported Aster private operation"})
		return
	}
	request, err := asterlive.BuildPrivatePayload(asterlive.PrivateRequestParams{
		Operation: operation, User: input.Account, Signer: input.Agent,
		Symbol: input.Symbol, ClientOrderID: input.ClientOrderID, Leverage: input.Leverage,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	authorized, err := s.live.agentAuthorizationMatches(r.Context(), "aster", request.Account, request.Signer)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify Aster authorization"})
		return
	}
	if !authorized {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Aster agent is not authorized for this owner"})
		return
	}
	s.live.signingStore.Store(request)
	writeJSON(w, http.StatusOK, request)
}

func (s *Server) handleAsterPrivateSubmit(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.signingStore == nil || s.live.asterPrivate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Aster private requests unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAuthorizationBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var signed domain.SignedAction
	if err := decoder.Decode(&signed); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	request, err := s.live.signingStore.Validate(signed)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	operation, ok := asterlive.ParsePrivateOperation(request.Action)
	if !ok || request.Venue != "aster" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "request is not an Aster private operation"})
		return
	}
	unlockOwner, err := s.live.lockAgentOwner("aster", request.Account)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Aster owner is busy; retry request"})
		return
	}
	defer unlockOwner()
	authorized, err := s.live.agentAuthorizationMatches(r.Context(), "aster", request.Account, signed.SignerAddress)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify Aster authorization"})
		return
	}
	if !authorized {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Aster agent authorization changed; prepare again"})
		return
	}
	request, err = s.live.signingStore.ValidateAndConsume(signed)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Aster signing request was already consumed"})
		return
	}
	result, err := s.live.asterPrivate.SubmitSignedPrivate(r.Context(), signed, request)
	if err != nil {
		if errors.Is(err, asterlive.ErrSubmissionNotSent) {
			s.live.signingStore.Store(request)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, asterlive.ErrSubmissionAmbiguous) {
			writeJSON(w, http.StatusAccepted, map[string]any{
				"request_id": signed.RequestID, "operation": operation,
				"uncertain": true, "error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	stateApplied, applyErr := s.live.applyAsterPrivateResult(request, result)
	if applyErr != nil && s.logger != nil {
		s.logger.Error("apply Aster private result", "operation", operation, "account", request.Account, "err", applyErr)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"request_id": signed.RequestID, "operation": operation, "data": result.Data,
		"state_applied": stateApplied,
		"submitted_at":  result.SubmittedAt, "responded_at": result.RespondedAt,
	})
}
