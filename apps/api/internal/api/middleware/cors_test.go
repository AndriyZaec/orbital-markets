package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSAllowsAnalyticsTokenHeader(t *testing.T) {
	handler := CORS("https://app.orbitalmarkets.xyz")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/analytics", nil)
	req.Header.Set("Origin", "https://app.orbitalmarkets.xyz")
	req.Header.Set("Access-Control-Request-Headers", "x-analytics-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	allowedHeaders := strings.ToLower(recorder.Header().Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowedHeaders, "x-analytics-token") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want X-Analytics-Token", allowedHeaders)
	}
}
