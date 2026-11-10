package api

import (
	"bytes"
	"encoding/json"
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
	if err != nil || len(bindings.Accounts) == 0 || bindings.requireAccountsWithin(supportedAccountVenues) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account bindings for supported live venues required"})
		return
	}
	views := newAccountViews(s.live.accounts, bindings.Accounts)
	defer views.accounts.Release()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := streamController(w)
	if err := flushStream(controller); err != nil {
		return
	}

	var lastVersion []byte
	emitBalances := func() bool {
		snapshots := views.snapshots()
		statuses := accountViewStatuses(snapshots)
		versionInput := make(map[string]any, len(bindings.Accounts)*2)
		for venue, snapshot := range snapshots {
			versionInput[venue] = snapshot
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
		if err := writeStream(w, controller, "event: balances\ndata: %s\n\n", data); err != nil {
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
			if err := writeStream(w, controller, ": heartbeat\n\n"); err != nil {
				return
			}
		case <-ticker.C:
			views.retryMissing(time.Now())
			if !emitBalances() {
				return
			}
		}
	}
}
