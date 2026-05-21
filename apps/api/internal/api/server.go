package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/paper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

// LiveDeps holds optional dependencies for live (non-custodial) execution.
// When nil, live endpoints return 503. This lets the server start without
// live venue clients configured.
type LiveDeps struct {
	signingStore *domain.SigningRequestStore
	pacClient    *paclive.Client
	pacTracker   *paclive.Tracker
	hlClient     *hllive.Client
	hlAssetMap   hllive.AssetMap
}

// NewLiveDeps creates a LiveDeps with the signing store (required)
// and optional venue clients. Venue clients can be nil until configured.
func NewLiveDeps(
	signingStore *domain.SigningRequestStore,
	pacClient *paclive.Client,
	pacTracker *paclive.Tracker,
	hlClient *hllive.Client,
	hlAssetMap hllive.AssetMap,
) *LiveDeps {
	return &LiveDeps{
		signingStore: signingStore,
		pacClient:    pacClient,
		pacTracker:   pacTracker,
		hlClient:     hlClient,
		hlAssetMap:   hlAssetMap,
	}
}

type Server struct {
	ctx      context.Context // server-lifetime context, not per-request
	scanner  *scanner.Scanner
	executor *paper.Executor
	store    *paper.DBStore
	db       *sql.DB
	live     *LiveDeps // nil = live endpoints disabled
	logger   *slog.Logger
	mux      *http.ServeMux
}

func NewServer(
	ctx context.Context,
	logger *slog.Logger,
	sc *scanner.Scanner,
	executor *paper.Executor,
	store *paper.DBStore,
	database *sql.DB,
	live *LiveDeps,
) *Server {
	s := &Server{
		ctx:      ctx,
		scanner:  sc,
		executor: executor,
		store:    store,
		db:       database,
		live:     live,
		logger:   logger,
		mux:      http.NewServeMux(),
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
	s.mux.HandleFunc("POST /api/v1/plan", s.handleBuildPlan)

	// Paper trading
	s.mux.HandleFunc("POST /api/v1/paper/open", s.handlePaperOpen)
	s.mux.HandleFunc("GET /api/v1/paper/positions", s.handlePaperPositions)
	s.mux.HandleFunc("GET /api/v1/paper/positions/", s.handlePaperPosition)
	s.mux.HandleFunc("POST /api/v1/paper/close/", s.handlePaperClose)

	// Analytics
	s.mux.HandleFunc("GET /api/v1/paper/analytics", s.handlePaperAnalytics)

	// Historical data
	s.mux.HandleFunc("GET /api/v1/history", s.handleHistory)

	// Live execution (non-custodial signing flow)
	s.mux.HandleFunc("POST /api/v1/live/prepare", s.handleLivePrepare)
	s.mux.HandleFunc("POST /api/v1/live/submit", s.handleLiveSubmit)
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

func (s *Server) handleBuildPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OpportunityID string  `json:"opportunity_id"`
		Leverage      float64 `json:"leverage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.OpportunityID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "opportunity_id required"})
		return
	}

	plan, err := s.scanner.BuildPlan(r.Context(), req.OpportunityID, req.Leverage)
	if err != nil {
		s.logger.Error("build plan", "err", err)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
