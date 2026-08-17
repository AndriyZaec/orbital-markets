package telegrambot

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
)

func TestLinkIntentIsHashedSingleUseAndPersistsAccountWatch(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewLinkService(database, "@orbital_test_bot")
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	deepLink, expiresAt, err := service.CreateLinkIntent(context.Background(), "sol-account", "0xABC")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(deepLink)
	if err != nil {
		t.Fatal(err)
	}
	token := parsed.Query().Get("start")
	if parsed.Host != "t.me" || parsed.Path != "/orbital_test_bot" || token == "" {
		t.Fatalf("deep link = %q", deepLink)
	}
	if !expiresAt.Equal(now.Add(linkIntentTTL)) {
		t.Fatalf("expires at = %v", expiresAt)
	}
	var rawTokenCount int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM telegram_link_intents WHERE token_hash = ?`, token,
	).Scan(&rawTokenCount); err != nil {
		t.Fatal(err)
	}
	if rawTokenCount != 0 {
		t.Fatal("raw Telegram link token was persisted")
	}

	link, err := service.ConsumeLinkIntent(context.Background(), token, 42)
	if err != nil {
		t.Fatal(err)
	}
	if link.ChatID != 42 || link.AccountPacifica != "sol-account" || link.AccountHyperliquid != "0xabc" {
		t.Fatalf("account link = %+v", link)
	}
	if _, err := service.ConsumeLinkIntent(context.Background(), token, 7); !errors.Is(err, ErrInvalidLinkToken) {
		t.Fatalf("second consume error = %v", err)
	}
	persisted, ok, err := service.AccountLink(context.Background(), 42)
	if err != nil || !ok || persisted.AccountPacifica != "sol-account" {
		t.Fatalf("persisted link = %+v, ok = %v, err = %v", persisted, ok, err)
	}

	unlinked, err := service.Unlink(context.Background(), 42)
	if err != nil || !unlinked {
		t.Fatalf("unlink = %v, err = %v", unlinked, err)
	}
	if _, ok, err := service.AccountLink(context.Background(), 42); err != nil || ok {
		t.Fatalf("link remains after unlink: ok = %v, err = %v", ok, err)
	}
}

func TestExpiredLinkIntentCannotBeConsumed(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "telegram-expired.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewLinkService(database, "orbital_test_bot")
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	deepLink, _, err := service.CreateLinkIntent(context.Background(), "sol-account", "0xabc")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(deepLink)
	service.now = func() time.Time { return now.Add(linkIntentTTL) }
	if _, err := service.ConsumeLinkIntent(context.Background(), parsed.Query().Get("start"), 42); !errors.Is(err, ErrInvalidLinkToken) {
		t.Fatalf("expired consume error = %v", err)
	}
}

func TestRelinkingChatReplacesWatchedAccounts(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "telegram-relink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewLinkService(database, "orbital_test_bot")
	ctx := context.Background()

	for _, accounts := range [][2]string{{"sol-a", "0xaaa"}, {"sol-b", "0xbbb"}} {
		deepLink, _, err := service.CreateLinkIntent(ctx, accounts[0], accounts[1])
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(deepLink)
		if _, err := service.ConsumeLinkIntent(ctx, parsed.Query().Get("start"), 42); err != nil {
			t.Fatal(err)
		}
	}

	link, ok, err := service.AccountLink(ctx, 42)
	if err != nil || !ok || link.AccountPacifica != "sol-b" || link.AccountHyperliquid != "0xbbb" {
		t.Fatalf("relinked account = %+v, ok = %v, err = %v", link, ok, err)
	}
}

func TestLinkIntentWritesAreRateLimited(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "telegram-rate-limit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewLinkService(database, "orbital_test_bot")
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	for range maxLinkIntentsPerWindow {
		if _, _, err := service.CreateLinkIntent(context.Background(), "sol-account", "0xabc"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := service.CreateLinkIntent(context.Background(), "sol-account", "0xabc"); !errors.Is(err, ErrLinkRateLimited) {
		t.Fatalf("link intent beyond fixed window error = %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM telegram_link_intents`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != maxLinkIntentsPerWindow {
		t.Fatalf("link intents = %d, want %d persisted writes", count, maxLinkIntentsPerWindow)
	}
	now = now.Add(-time.Hour)
	if _, _, err := service.CreateLinkIntent(context.Background(), "sol-account", "0xabc"); err != nil {
		t.Fatalf("link intent after backward clock adjustment: %v", err)
	}
}
