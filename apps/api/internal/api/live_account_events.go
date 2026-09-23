package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	liveAccountEventTick      = time.Second
	liveAccountEventHeartbeat = 15 * time.Second
)

func (s *Server) handleLiveAccountEvents(w http.ResponseWriter, r *http.Request) {
	if s.live == nil || s.live.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live execution not configured"})
		return
	}
	bindings, err := liveVenueBindingsFromQuery(r.URL.Query())
	if err != nil || len(bindings.Accounts) == 0 || bindings.requireAccountsWithin(supportedLiveVenues) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account bindings for supported live venues required"})
		return
	}
	accounts, err := s.live.acquireAccountContext(bindings.Accounts, false)
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

	var lastVersion []byte
	emitBalances := func() bool {
		statuses := make(map[string]venueAccountStatus, len(bindings.Accounts))
		versionInput := make(map[string]any, len(bindings.Accounts)*2)
		for venue := range bindings.Accounts {
			feed, found := accounts.Feed(venue)
			if !found {
				return true
			}
			statuses[venue] = accountStatus(accounts, venue, displayFreshness)
			versionInput[venue] = feed.Snapshot()
			versionInput[venue+"_fresh"] = statuses[venue].Fresh
		}
		version, err := json.Marshal(versionInput)
		if err != nil || bytes.Equal(version, lastVersion) {
			return true
		}
		data, err := json.Marshal(statuses)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: balances\ndata: %s\n\n", data); err != nil || controller.Flush() != nil {
			return false
		}
		lastVersion = version
		return true
	}

	if !emitBalances() {
		return
	}
	ticker := time.NewTicker(liveAccountEventTick)
	heartbeat := time.NewTicker(liveAccountEventHeartbeat)
	defer ticker.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil || controller.Flush() != nil {
				return
			}
		case <-ticker.C:
			if !emitBalances() {
				return
			}
		}
	}
}
