package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	phoenixlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
)

func TestPhoenixLiveReadinessGatesOpenWithoutDisablingNativeSafety(t *testing.T) {
	var b *phoenixSubmitTestBackend
	f, _, _, _, feed := newPhoenixPrepareFixture(t, func(f *phoenixAccountFixture, base *phoenixOrderTestBackend, _ *phoenixOrderTestMarkets) {
		b = enablePhoenixSubmitTestBackend(t, f, base)
	})
	module, err := phoenixlive.NewLiveModule(context.Background(), &phoenixOrderPreparer{deps: f.server.live, config: f.factory.config, backend: b})
	if err != nil {
		t.Fatal(err)
	}
	f.server.live.modules, err = venue.NewLiveModuleRegistry(module)
	if err != nil {
		t.Fatal(err)
	}
	f.server.EnablePhoenixInfrastructure(b)
	if err := feed.refreshView(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snapshot := feed.Snapshot(); !snapshot.Phoenix.LiveReady || len(feed.PreTradeBlockers(domain.Leg{})) != 0 {
		t.Fatalf("full verified account blocked: %+v", snapshot.Phoenix)
	}
	checkGlobal := func(want bool) {
		t.Helper()
		w := httptest.NewRecorder()
		f.server.handlePhoenixInfrastructure(w, httptest.NewRequest("GET", "/api/v1/live/phoenix/status", nil))
		var status struct {
			LiveReady       bool `json:"live_ready"`
			AccountRequired bool `json:"account_verification_required"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.LiveReady != want || !status.AccountRequired {
			t.Fatalf("unexpected global readiness: %s", w.Body)
		}
	}
	checkGlobal(true)
	params := phoenixPrepareParams()
	params.Signer = f.backend.agent
	if _, err := module.BuildOpen(params); err != nil {
		t.Fatal("ready account could not prepare opening", err)
	}
	for _, gate := range []string{"infrastructure", "flight", "stale_verification"} {
		t.Run(gate, func(t *testing.T) {
			switch gate {
			case "infrastructure":
				b.denyInfrastructure.Store(true)
			case "flight":
				b.denyFlight.Store(true)
			}
			if err := feed.refreshView(context.Background()); err != nil {
				t.Fatal(err)
			}
			if gate == "stale_verification" {
				feed.mu.Lock()
				details := *feed.snapshot.Phoenix
				details.executionCheckedAt = time.Now().Add(-31 * time.Second)
				feed.snapshot.Phoenix = &details
				feed.mu.Unlock()
			} else {
				checkGlobal(false)
			}
			if snapshot := feed.Snapshot(); !snapshot.Phoenix.AccountReady || snapshot.Phoenix.LiveReady || len(feed.PreTradeBlockers(domain.Leg{})) == 0 {
				t.Fatalf("gate did not block live opening independently of account data: %+v", snapshot.Phoenix)
			}
			if _, err := module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: params, Action: venue.ReduceActionEmergencyClose}); err != nil {
				t.Fatal("opening infrastructure/Flight diagnostic blocked native safety", err)
			}
			b.denyInfrastructure.Store(false)
			b.denyFlight.Store(false)
		})
	}
	f.server.invalidatePhoenixAccount(phoenixTestOwner)
	if feed.Snapshot().Phoenix.LiveReady {
		t.Fatal("authority invalidation retained live readiness")
	}
}
