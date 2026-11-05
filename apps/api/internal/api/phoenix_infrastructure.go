package api

import (
	"context"
	"net/http"

	phoenixruntime "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/runtime"
)

type PhoenixInfrastructure interface {
	Readiness(context.Context) phoenixruntime.Status
}

// EnablePhoenixInfrastructure must be called before serving requests. It adds
// infrastructure diagnostics only, not a live module or account-feed factory.
func (s *Server) EnablePhoenixInfrastructure(infrastructure PhoenixInfrastructure) {
	if infrastructure == nil {
		return
	}
	s.phoenixInfrastructure = infrastructure
}

func (s *Server) handlePhoenixInfrastructure(w http.ResponseWriter, r *http.Request) {
	status := phoenixruntime.Status{Reason: "not_configured"}
	configured := s.phoenixInfrastructure != nil
	if configured {
		status = s.phoenixInfrastructure.Readiness(r.Context())
	}
	flight := phoenixruntime.FlightStatus{Reason: "not_configured"}
	if provider, ok := s.phoenixInfrastructure.(interface {
		FlightReadiness() phoenixruntime.FlightStatus
	}); ok {
		flight = provider.FlightReadiness()
	}
	writeJSON(w, http.StatusOK, struct {
		Configured     bool                        `json:"configured"`
		Infrastructure phoenixruntime.Status       `json:"infrastructure"`
		Flight         phoenixruntime.FlightStatus `json:"flight"`
		LiveReady      bool                        `json:"live_ready"`
	}{Configured: configured, Infrastructure: status, Flight: flight, LiveReady: false})
}
