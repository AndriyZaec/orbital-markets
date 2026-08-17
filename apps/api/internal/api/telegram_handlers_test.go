package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/paper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/telegrambot"
)

type fakeTelegramLinker struct {
	pacifica    string
	hyperliquid string
	err         error
}

func (f *fakeTelegramLinker) CreateLinkIntent(
	_ context.Context, pacifica, hyperliquid string,
) (string, time.Time, error) {
	f.pacifica = pacifica
	f.hyperliquid = hyperliquid
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return "https://t.me/orbital_bot?start=token", time.Date(2026, 8, 17, 12, 10, 0, 0, time.UTC), nil
}

func TestTelegramLinkIntentUsesConnectedVenueAccounts(t *testing.T) {
	links := &fakeTelegramLinker{}
	server := &Server{
		telegramLinks: links,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/link-intents", strings.NewReader(`{
		"account_pacifica":" sol-account ",
		"account_hyperliquid":" 0xABC "
	}`))
	response := httptest.NewRecorder()
	server.handleTelegramLinkIntent(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if links.pacifica != "sol-account" || links.hyperliquid != "0xABC" {
		t.Fatalf("link accounts = %q, %q", links.pacifica, links.hyperliquid)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["url"] != "https://t.me/orbital_bot?start=token" || body["expires_at"] == "" {
		t.Fatalf("response = %v", body)
	}
}

func TestTelegramLinkIntentRejectsMissingAccount(t *testing.T) {
	server := &Server{
		telegramLinks: &fakeTelegramLinker{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/link-intents", strings.NewReader(`{"account_pacifica":"sol-account"}`))
	response := httptest.NewRecorder()
	server.handleTelegramLinkIntent(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestTelegramLinkIntentReturnsRetryableRateLimit(t *testing.T) {
	server := &Server{
		telegramLinks: &fakeTelegramLinker{err: telegrambot.ErrLinkRateLimited},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/link-intents", strings.NewReader(`{
		"account_pacifica":"sol-account",
		"account_hyperliquid":"0xabc"
	}`))
	response := httptest.NewRecorder()
	server.handleTelegramLinkIntent(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("status = %d, retry-after = %q", response.Code, response.Header().Get("Retry-After"))
	}
}

func TestTelegramLinkIntentRouteRemainsBehindJWTMiddleware(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "telegram-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sc := scanner.New(logger)
	store := paper.NewDBStore(database)
	server := NewServer(
		context.Background(), logger, sc, paper.NewExecutor(logger, store, sc), store,
		database, nil, "jwt-secret", "",
	)
	server.EnableTelegramLinks(&fakeTelegramLinker{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/link-intents", strings.NewReader(`{
		"account_pacifica":"sol-account",
		"account_hyperliquid":"0xabc"
	}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unauthenticated status = %d, want 404", response.Code)
	}
}
