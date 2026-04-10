package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

type Server struct {
	scanner *scanner.Scanner
	logger  *slog.Logger
	mux     *http.ServeMux
}

func NewServer(logger *slog.Logger, sc *scanner.Scanner) *Server {
	s := &Server{
		scanner: sc,
		logger:  logger,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/markets", s.handleMarkets)
	s.mux.HandleFunc("GET /api/v1/opportunities", s.handleOpportunities)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMarkets(w http.ResponseWriter, r *http.Request) {
	data := s.scanner.MarketData(r.Context())
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleOpportunities(w http.ResponseWriter, r *http.Request) {
	opps := s.scanner.Opportunities()
	writeJSON(w, http.StatusOK, opps)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
