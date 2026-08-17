package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

func TestRootHandlerReturnsAPIDirectlyWhenTelegramDisabled(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "")
	t.Setenv("TELEGRAM_BOT_USERNAME", "")
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	integration := buildTelegramIntegration(testLogger(), scanner.New(testLogger()), nil)
	handler := buildRootHandler(apiHandler, integration)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("API status = %d, want 204", response.Code)
	}
}

func TestRootHandlerSeparatesTelegramWebhookFromAPI(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "secret")
	t.Setenv("TELEGRAM_BOT_USERNAME", "")
	apiCalls := 0
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalls++
		w.WriteHeader(http.StatusNoContent)
	})
	integration := buildTelegramIntegration(testLogger(), scanner.New(testLogger()), nil)
	handler := buildRootHandler(apiHandler, integration)

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusNoContent || apiCalls != 1 {
		t.Fatalf("API status = %d, calls = %d", apiResponse.Code, apiCalls)
	}

	webhookRequest := httptest.NewRequest(http.MethodPost, "/telegram/webhook", strings.NewReader("invalid"))
	webhookRequest.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	webhookResponse := httptest.NewRecorder()
	handler.ServeHTTP(webhookResponse, webhookRequest)
	if webhookResponse.Code != http.StatusBadRequest {
		t.Fatalf("webhook status = %d, want 400", webhookResponse.Code)
	}
	if apiCalls != 1 {
		t.Fatalf("webhook reached API fallback; calls = %d", apiCalls)
	}
}

func TestRootHandlerDoesNotExposeMisconfiguredTelegramRoute(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "")
	t.Setenv("TELEGRAM_BOT_USERNAME", "")
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	integration := buildTelegramIntegration(testLogger(), scanner.New(testLogger()), nil)
	handler := buildRootHandler(apiHandler, integration)

	request := httptest.NewRequest(http.MethodPost, "/telegram/webhook", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want API fallback status 418", response.Code)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
