package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const (
	liveEventTick      = time.Second
	liveEventHeartbeat = 15 * time.Second
	positionEventEvery = 5
)

func (s *Server) handleLiveEvents(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live execution not configured"})
		return
	}
	pacificaAccount, hyperliquidAccount, ok := liveAccountsFromQuery(w, r)
	if !ok {
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID != "" {
		if _, err := s.liveSessionStatusSnapshot(r.Context(), sessionID, pacificaAccount, hyperliquidAccount); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "live session not found"})
			return
		}
	}

	accounts, err := s.live.acquireAccounts(pacificaAccount, hyperliquidAccount)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	defer accounts.Release()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}

	var lastBalanceVersion, lastBalances, lastPositions, lastSession []byte
	emitChanged := func(event string, value any, previous *[]byte) bool {
		data, err := json.Marshal(value)
		if err != nil || bytes.Equal(data, *previous) {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		if err := controller.Flush(); err != nil {
			return false
		}
		*previous = data
		return true
	}

	emitBalances := func() bool {
		pacificaFeed, pacificaOK := accounts.Feed("pacifica")
		hyperliquidFeed, hyperliquidOK := accounts.Feed("hyperliquid")
		if !pacificaOK || !hyperliquidOK {
			return true
		}
		pacificaSnapshot := pacificaFeed.Snapshot()
		hyperliquidSnapshot := hyperliquidFeed.Snapshot()
		pac, hl := liveAccountStatuses(accounts, displayFreshness)
		version, err := json.Marshal(map[string]any{
			"pacifica":          pacificaSnapshot,
			"hyperliquid":       hyperliquidSnapshot,
			"pacifica_fresh":    pac.Fresh,
			"hyperliquid_fresh": hl.Fresh,
		})
		if err != nil || bytes.Equal(version, lastBalanceVersion) {
			return true
		}
		lastBalanceVersion = version
		return emitChanged("balances", map[string]any{"pacifica": pac, "hyperliquid": hl}, &lastBalances)
	}
	emitPositions := func() bool {
		positions, err := s.liveStore.ListPositionsForAccounts(r.Context(), pacificaAccount, hyperliquidAccount)
		if err != nil {
			return true
		}
		if positions == nil {
			positions = []executor.LivePosition{}
		}
		return emitChanged("positions", positions, &lastPositions)
	}
	emitSession := func() bool {
		if sessionID == "" {
			return true
		}
		status, err := s.liveSessionStatusSnapshot(r.Context(), sessionID, pacificaAccount, hyperliquidAccount)
		if err != nil {
			return true
		}
		return emitChanged("session", status, &lastSession)
	}

	if !emitBalances() || !emitPositions() || !emitSession() {
		return
	}
	ticker := time.NewTicker(liveEventTick)
	heartbeat := time.NewTicker(liveEventHeartbeat)
	defer ticker.Stop()
	defer heartbeat.Stop()
	ticks := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil || controller.Flush() != nil {
				return
			}
		case <-ticker.C:
			ticks++
			if !emitBalances() || !emitSession() {
				return
			}
			if ticks%positionEventEvery == 0 && !emitPositions() {
				return
			}
		}
	}
}
