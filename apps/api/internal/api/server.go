package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api/middleware"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/paper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

type Server struct {
	ctx       context.Context // server-lifetime context, not per-request
	scanner   *scanner.Scanner
	executor  *paper.Executor
	store     *paper.DBStore
	db        *sql.DB
	liveStore *executor.Store // always available when DB exists — read-only live position access
	live      *LiveDeps       // nil = live execution endpoints disabled (venue clients not configured)
	logger    *slog.Logger
	mux       *http.ServeMux
	handler   http.Handler // mux wrapped in middleware (recovery → logging → auth)
}

func NewServer(
	ctx context.Context,
	logger *slog.Logger,
	sc *scanner.Scanner,
	exec *paper.Executor,
	store *paper.DBStore,
	database *sql.DB,
	live *LiveDeps,
	jwtSecret string,
	corsOrigin string,
) *Server {
	// Live position store is always available for reads, even without venue clients.
	var ls *executor.Store
	if live != nil && live.liveStore != nil {
		ls = live.liveStore
	} else {
		ls = executor.NewStore(database, logger)
	}

	s := &Server{
		ctx:       ctx,
		scanner:   sc,
		executor:  exec,
		store:     store,
		db:        database,
		liveStore: ls,
		live:      live,
		logger:    logger,
		mux:       http.NewServeMux(),
	}
	s.routes()

	// Middleware order on the request path: recovery → logging → CORS → auth → mux.
	// CORS sits before auth so preflight OPTIONS can succeed without a cookie.
	s.handler = middleware.Recovery(logger)(
		middleware.Logging(logger)(
			middleware.CORS(corsOrigin)(
				middleware.Auth(jwtSecret, logger)(s.mux),
			),
		),
	)
	return s
}

func (s *Server) Handler() http.Handler {
	return s.handler
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
	s.mux.HandleFunc("POST /api/v1/live/advance", s.handleLiveAdvance)
	s.mux.HandleFunc("POST /api/v1/live/submit", s.handleLiveSubmit)
	s.mux.HandleFunc("GET /api/v1/live/balances", s.handleLiveBalances)
	s.mux.HandleFunc("POST /api/v1/live/accounts/ensure", s.handleLiveAccountsEnsure)
	s.mux.HandleFunc("GET /api/v1/live/positions", s.handleLivePositions)
	s.mux.HandleFunc("GET /api/v1/live/positions/", s.handleLivePosition)
	s.mux.HandleFunc("POST /api/v1/live/close/", s.handleLiveClose)
	s.mux.HandleFunc("POST /api/v1/live/kill", s.handleLiveKill)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMarkets(w http.ResponseWriter, r *http.Request) {
	data := s.scanner.MarketData(r.Context())
	writeJSON(w, http.StatusOK, data)
}

// Bounds for the ?limit query on GET /api/v1/opportunities. Default keeps the
// default response small (~100 rows) since the UI's default table shows far
// fewer; the cap protects against pathological requests. Invalid or missing
// values fall back to the default — never rejected.
const (
	opportunitiesDefaultLimit = 100
	opportunitiesMaxLimit     = 300
)

func (s *Server) handleOpportunities(w http.ResponseWriter, r *http.Request) {
	limit := opportunitiesDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
			if limit > opportunitiesMaxLimit {
				limit = opportunitiesMaxLimit
			}
		}
	}
	opps := s.scanner.Opportunities()
	if len(opps) > limit {
		opps = opps[:limit]
	}
	writeJSON(w, http.StatusOK, opps)
}

func (s *Server) handleBuildPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OpportunityID     string   `json:"opportunity_id"`
		Leverage          float64  `json:"leverage"`
		RequestedNotional *float64 `json:"requested_notional,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.OpportunityID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "opportunity_id required"})
		return
	}
	// Explicitly-supplied notional must be positive; absent is fine (falls back to recommended).
	if req.RequestedNotional != nil && *req.RequestedNotional <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "requested_notional must be positive"})
		return
	}
	var notional float64
	if req.RequestedNotional != nil {
		notional = *req.RequestedNotional
	}
	plan, err := s.scanner.BuildPlan(r.Context(), req.OpportunityID, req.Leverage, notional)
	if err != nil {
		s.logger.Error("build plan", "err", err)
		writePlanError(w, http.StatusUnprocessableEntity, err)
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

func writePlanError(w http.ResponseWriter, status int, err error) {
	var leverageErr *scanner.LeverageRangeError
	if errors.As(err, &leverageErr) {
		writeJSON(w, status, map[string]any{
			"error":             err.Error(),
			"pair_max_leverage": leverageErr.PairMax,
		})
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
