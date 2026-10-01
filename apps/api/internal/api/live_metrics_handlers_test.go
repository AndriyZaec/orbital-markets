package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/analytics"
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
			server := &Server{analyticsDB: database, logger: slog.New(slog.NewTextHandler(&logs, nil))}
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

func TestWeeklyAPRLimitsHistoryAndCachesReport(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "weekly.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	now := time.Now().UTC()
	weekStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -(int(now.Weekday())+6)%7)
	for week := range 4 {
		bucket := weekStart.AddDate(0, 0, -7*week).Add(time.Hour).Unix()
		asset := "WEEK" + string(rune('A'+week))
		for _, venue := range []string{"aster", "hyperliquid"} {
			if _, err := database.Exec(`
				INSERT INTO market_snapshots_1h
					(venue, asset, bucket_unix, open, high, low, close, funding_avg, oi_avg, bid_avg, ask_avg)
				VALUES (?, ?, ?, 1, 1, 1, 1, 0.001, 1, 1, 1)`, venue, asset, bucket); err != nil {
				t.Fatal(err)
			}
		}
	}

	server := &Server{analyticsDB: database, logger: slog.Default()}
	first := httptest.NewRecorder()
	server.handleWeeklyAPR(first, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/weekly-apr", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d: %s", first.Code, first.Body.String())
	}
	var report analytics.WeeklyAPRReport
	if err := json.Unmarshal(first.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 3 {
		t.Fatalf("weekly rows = %d, want 3", len(report.Rows))
	}

	if _, err := database.Exec("DELETE FROM market_snapshots_1h"); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	server.handleWeeklyAPR(second, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/weekly-apr", nil))
	if second.Body.String() != first.Body.String() {
		t.Fatalf("second report bypassed cache:\nfirst: %s\nsecond: %s", first.Body.String(), second.Body.String())
	}
}
