package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
)

func TestPublicMetricsExposeOnlyTotalVolume(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	server := &Server{db: database}
	response := httptest.NewRecorder()

	server.handlePublicMetrics(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/metrics", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); body != "{\"total_volume\":\"0.00\"}\n" {
		t.Fatalf("unexpected public metrics: %s", body)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "*" ||
		!strings.Contains(response.Header().Get("Cache-Control"), "max-age=60") {
		t.Fatalf("headers = %v", response.Header())
	}
}
