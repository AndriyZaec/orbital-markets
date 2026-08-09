package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func (s *Server) handleLiveSessionStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/live/sessions/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session id required"})
		return
	}

	pacificaAccount, hyperliquidAccount, ok := liveAccountsFromQuery(w, r)
	if !ok {
		return
	}
	response, err := s.liveSessionStatusSnapshot(r.Context(), id, pacificaAccount, hyperliquidAccount)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "live session not found"})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) liveSessionStatusSnapshot(
	ctx context.Context,
	id, pacificaAccount, hyperliquidAccount string,
) (map[string]any, error) {
	record, err := s.liveStore.GetDurableSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(record.AccountPacifica) != strings.TrimSpace(pacificaAccount) ||
		!strings.EqualFold(strings.TrimSpace(record.AccountHyperliquid), strings.TrimSpace(hyperliquidAccount)) {
		return nil, errors.New("live session account mismatch")
	}

	status := publicLiveSessionStatus(record.State, record.Terminal)
	response := map[string]any{
		"session_id": id,
		"status":     status,
		"reason":     record.RecoveryDetail,
	}
	if status == string(sessAborted) {
		response["unwound"] = true
		response["unwind_status"] = string(unwindConfirmed)
	}

	session, err := unmarshalLiveSession(record.Payload)
	if err != nil || session.Plan == nil {
		return response, nil
	}
	position, err := s.liveStore.GetPositionForAccounts(
		ctx, session.Plan.ID, session.AccountPacifica, session.AccountHyperliquid,
	)
	if err != nil {
		return response, nil
	}
	if record.State == "already_persisted" {
		status = publicPersistedPositionStatus(position.State)
		response["status"] = status
	}
	response["position_id"] = position.ID
	response["mismatch"] = position.HedgeMismatch

	fills, err := s.liveStore.GetFills(ctx, position.ID)
	if err == nil {
		remaining := make([]map[string]any, 0, len(fills))
		for _, fill := range fills {
			view := persistedFillView(fill)
			switch fill.Leg {
			case 1:
				response["leg1_fill"] = view
			case 2:
				response["leg2_fill"] = view
			}
			if status == string(sessDegraded) && fill.Filled && fill.FilledAmount > 0 {
				remaining = append(remaining, map[string]any{
					"leg": fill.Leg, "venue": fill.Venue, "symbol": fill.Symbol,
					"side": fill.Side, "amount": fill.FilledAmount,
				})
			}
		}
		response["remaining_exposure"] = remaining
	}

	return response, nil
}

func publicPersistedPositionStatus(state string) string {
	switch state {
	case string(executor.ExecStateOpen):
		return string(sessOpen)
	case string(executor.ExecStateDegraded):
		return string(sessDegraded)
	default:
		return string(sessFailed)
	}
}

func publicLiveSessionStatus(state string, terminal bool) string {
	switch sessionState(state) {
	case "recovery_blocked":
		return string(sessDegraded)
	}
	if !terminal {
		return string(sessRecovering)
	}
	switch sessionState(state) {
	case sessOpen, sessDegraded, sessAborted, sessFailed:
		return state
	default:
		return string(sessFailed)
	}
}

func persistedFillView(fill executor.LiveFill) map[string]any {
	status := "unconfirmed"
	if fill.Filled {
		status = "filled"
	}
	return map[string]any{
		"filled_amount": fill.FilledAmount,
		"avg_price":     fill.AvgFillPrice,
		"fill_ratio":    fill.FillRatio,
		"status":        status,
	}
}
