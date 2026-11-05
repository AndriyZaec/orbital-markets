package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	phoenixruntime "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/runtime"
)

type infrastructureStatus phoenixruntime.Status

func (s infrastructureStatus) Readiness(context.Context) phoenixruntime.Status {
	return phoenixruntime.Status(s)
}

type verifiedFlightInfrastructure struct{ infrastructureStatus }

func (verifiedFlightInfrastructure) FlightReadiness() phoenixruntime.FlightStatus {
	return phoenixruntime.FlightStatus{Ready: true, BuilderAuthority: "configured-public-builder"}
}

func TestPhoenixInfrastructureNeverAdvertisesLiveReady(t *testing.T) {
	for _, provider := range []PhoenixInfrastructure{nil, infrastructureStatus{Ready: true}, infrastructureStatus{Reason: "rpc_unavailable"}, verifiedFlightInfrastructure{infrastructureStatus{Ready: true}}} {
		s := &Server{}
		s.EnablePhoenixInfrastructure(provider)
		w := httptest.NewRecorder()
		s.handlePhoenixInfrastructure(w, httptest.NewRequest("GET", "/api/v1/live/phoenix/status", nil))
		var result struct {
			Configured     bool                        `json:"configured"`
			Infrastructure phoenixruntime.Status       `json:"infrastructure"`
			Flight         phoenixruntime.FlightStatus `json:"flight"`
			LiveReady      bool                        `json:"live_ready"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || result.LiveReady || result.Configured != (provider != nil) {
			t.Fatalf("unexpected status: %s", w.Body)
		}
		if provider != nil && result.Infrastructure != provider.Readiness(context.Background()) {
			t.Fatalf("infrastructure status lost: %s", w.Body)
		}
		if provider == nil && result.Infrastructure.Reason != "not_configured" {
			t.Fatalf("missing disabled reason: %s", w.Body)
		}
		if _, flight := provider.(verifiedFlightInfrastructure); result.Flight.Ready != flight {
			t.Fatalf("Flight readiness was confused with infrastructure/live readiness: %s", w.Body)
		}
	}
}

func TestPhoenixStatusUsesNormalAPIAuthentication(t *testing.T) {
	for _, secret := range []string{"beta-secret", ""} {
		s := NewServer(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, secret, "")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/live/phoenix/status", nil))
		want := 200
		if secret != "" {
			want = 404
		}
		if w.Code != want {
			t.Fatalf("status route returned %d want %d", w.Code, want)
		}
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/health", nil))
		if w.Code != 200 {
			t.Fatalf("global health returned %d", w.Code)
		}
	}
}
