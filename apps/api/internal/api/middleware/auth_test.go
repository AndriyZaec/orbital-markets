package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStagingAuthIsolatesCookieAndSigningSecret(t *testing.T) {
	token := func(secret string) string {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":4102444800}`))
		message := header + "." + payload
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(message))
		return message + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	prod, stage := token("prod-secret"), token("staging-secret")
	for _, tc := range []struct {
		name, secret, cookieName, cookie, bearer string
		want                                     int
	}{
		{"production default", "prod-secret", "", "__beta=" + prod, "", 204},
		{"staging cookie", "staging-secret", "__beta_staging", "__beta_staging=" + stage, "", 204},
		{"production cookie cannot enter staging", "staging-secret", "__beta_staging", "__beta=" + prod, "", 404},
		{"staging token needs staging cookie", "staging-secret", "__beta_staging", "__beta=" + stage, "", 404},
		{"production token cannot enter staging", "staging-secret", "__beta_staging", "__beta_staging=" + prod, "", 404},
		{"both cookies", "staging-secret", "__beta_staging", "__beta=" + prod + "; __beta_staging=" + stage, "", 204},
		{"staging cannot enter production", "prod-secret", "", "__beta_staging=" + stage, "", 404},
		{"operator bearer", "staging-secret", "__beta_staging", "__beta=" + prod, stage, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler := Auth(tc.secret, logger, tc.cookieName)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			request := httptest.NewRequest(http.MethodGet, "/api/v1/access", nil)
			request.Header.Set("Cookie", tc.cookie)
			if tc.bearer != "" {
				request.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestAuthAllowsAnalyticsHandlerToAuthenticateServiceToken(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Auth("jwt-secret", logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/analytics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAuthAllowsWeeklyAPRHandlerToAuthenticateServiceToken(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Auth("jwt-secret", logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/weekly-apr", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAuthAllowsDeliberatelyPublicMetrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Auth("jwt-secret", logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/public/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAuthProtectsAccessProbe(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := Auth("jwt-secret", logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/access", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
