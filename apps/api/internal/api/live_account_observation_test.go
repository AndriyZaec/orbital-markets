package api

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountObservationSurvivesReadinessFailureForEveryVenue(t *testing.T) {
	for _, venue := range []string{"pacifica", "hyperliquid", "aster", "phoenix"} {
		t.Run(venue, func(t *testing.T) {
			at := time.Now().Add(-time.Second).UTC()
			status := buildVenueAccountStatusFromSnapshot(venue, liveAccountSnapshot{
				Venue: venue, Connected: true, LastUpdated: at, Equity: 100, Available: 80,
				UnavailableReason: "risk refresh required",
			}, admissionFreshness)
			raw, _ := json.Marshal(status)
			var body map[string]json.RawMessage
			_ = json.Unmarshal(raw, &body)
			var observation struct {
				Equity, Available float64
				ObservedAt        time.Time `json:"observed_at"`
			}
			if json.Unmarshal(body["observation"], &observation) != nil || observation.Equity != 100 || observation.Available != 80 || !observation.ObservedAt.Equal(at) {
				t.Fatalf("lost observation during readiness failure: %s", raw)
			}
			if status.Fresh || !status.Connected || !status.StreamReady {
				t.Fatalf("readiness failure conflated with transport/initial loading: %+v", status)
			}
		})
	}
}

func TestEveryVenueRetainsExpiredObservationWithoutExtendingReadiness(t *testing.T) {
	for _, venue := range []string{"pacifica", "hyperliquid", "aster", "phoenix"} {
		status := buildVenueAccountStatusFromSnapshot(venue, liveAccountSnapshot{
			Venue: venue, Connected: true, LastUpdated: time.Now().Add(-45 * time.Second), Equity: 100, Available: 80,
		}, displayFreshness)
		if status.Fresh || !status.StreamReady || status.Observation == nil || status.Observation.Equity != 100 {
			t.Fatalf("%s observation became a cold account or extended readiness: %+v", venue, status)
		}
	}
}

func TestPhoenixPeriodicSnapshotsPreserveObservationWhileRefreshingCurrentRisk(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	defer lease.Release()
	for range 3 {
		previous := feed.Snapshot()
		started, unblock := make(chan struct{}, 1), make(chan struct{})
		var once sync.Once
		release := func() { once.Do(func() { close(unblock) }) }
		t.Cleanup(release)
		f.backend.mu.Lock()
		f.backend.onRisk = func() { started <- struct{}{}; <-unblock }
		f.backend.mu.Unlock()
		message := make(map[string]any)
		for key, value := range f.fullSnapshot {
			message[key] = value
		}
		message["slot"] = feed.state.Snapshot().Slot + 1
		f.send <- message
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("periodic snapshot did not refresh risk")
		}
		pending := feed.Snapshot()
		status := buildVenueAccountStatusFromSnapshot("phoenix", pending, admissionFreshness)
		if status.Observation == nil || status.Observation.Equity != previous.Equity || !status.Observation.ObservedAt.Equal(previous.LastUpdated) || !status.Connected || !status.StreamReady || status.Fresh {
			t.Fatalf("periodic slot became a cold account or admitted stale risk: %+v", status)
		}
		f.backend.mu.Lock()
		f.backend.onRisk = nil
		f.backend.mu.Unlock()
		release()
		waitPhoenix(t, func() bool { return feed.Snapshot().UnavailableReason == "" })
	}
	if f.wsCount.Load() != 1 || f.backend.authorityCalls.Load() != 0 {
		t.Fatal("background snapshots reconnected or issued authority RPC")
	}
}

func TestPhoenixSupersededRiskRefreshCapturesLatestAccountWithoutFailureBackoff(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	defer lease.Release()
	before := feed.state.Snapshot()
	var changed atomic.Bool
	f.backend.mu.Lock()
	f.backend.onRisk = func() {
		if !changed.CompareAndSwap(false, true) {
			return
		}
		f.send <- map[string]any{"channel": "traderState", "authority": phoenixTestOwner, "traderPdaIndex": 0,
			"slot": before.Slot + 1, "messageType": "delta", "deltas": []any{map[string]any{
				"subaccountIndex": 0, "sequence": before.Sequence + 1, "collateral": strconv.FormatUint(100000001, 10),
			}}}
		deadline := time.Now().Add(time.Second)
		for feed.state.Snapshot().Sequence == before.Sequence && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	f.backend.mu.Unlock()
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal("normal account progress became a failed refresh", err)
	}
	if got := feed.Snapshot(); got.UnavailableReason != "" || !got.Phoenix.AccountReady {
		t.Fatalf("latest refresh not published: %+v", got)
	}
	if f.backend.authorityCalls.Load() != 0 {
		t.Fatal("background refresh issued Solana authority RPC")
	}
}

func TestPhoenixRiskFailureRetainsLastSuccessfulObservation(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	defer lease.Release()
	before := feed.Snapshot()
	f.backend.mu.Lock()
	f.backend.riskErr = errors.New("risk unavailable")
	f.backend.mu.Unlock()
	if err := feed.refreshView(context.Background()); err == nil {
		t.Fatal("expected risk failure")
	}
	after := feed.Snapshot()
	if after.Equity != before.Equity || after.Available != before.Available || !after.LastUpdated.Equal(before.LastUpdated) {
		t.Fatalf("failed refresh erased or renewed money observation: before=%+v after=%+v", before, after)
	}
	if after.UnavailableReason == "" || after.Phoenix.AccountReady {
		t.Fatal("retained observation authorized execution")
	}
}
