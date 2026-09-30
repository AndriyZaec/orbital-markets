package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
)

func TestAnalyticsHandlersLogDatabaseFailures(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		message string
		repeat  bool
		handle  func(*Server, http.ResponseWriter, *http.Request)
	}{
		{name: "live analytics", path: "/api/v1/analytics/live", message: "live analytics: load failed", handle: (*Server).handleLiveAnalytics},
		{name: "weekly APR", path: "/api/v1/analytics/weekly-apr", message: "weekly APR: load failed", handle: (*Server).handleWeeklyAPR},
		{name: "public metrics", path: "/api/v1/public/metrics", message: "public metrics: load failed", repeat: true, handle: (*Server).handlePublicMetrics},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			server := &Server{db: database, logger: slog.New(slog.NewTextHandler(&logs, nil))}
			response := httptest.NewRecorder()
			test.handle(server, response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if test.repeat {
				test.handle(server, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, test.path, nil))
			}

			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusInternalServerError, response.Body.String())
			}
			if !strings.Contains(logs.String(), test.message) || !strings.Contains(logs.String(), "database is closed") {
				t.Fatalf("logs = %q, want handler context and database error", logs.String())
			}
			if test.repeat && strings.Count(logs.String(), test.message) != 1 {
				t.Fatalf("logs = %q, want one rate-limited error", logs.String())
			}
		})
	}
}

func TestWeeklyAPRDoesNotFallBackWhenAnalyticsIsolationIsUnavailable(t *testing.T) {
	var logs bytes.Buffer
	server := &Server{
		analyticsDBDisabled: true,
		logger:              slog.New(slog.NewTextHandler(&logs, nil)),
	}
	response := httptest.NewRecorder()

	server.handleWeeklyAPR(response, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/weekly-apr", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if !strings.Contains(logs.String(), "isolated analytics database unavailable") {
		t.Fatalf("logs = %q, want isolation error", logs.String())
	}
}
