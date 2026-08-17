package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/telegrambot"
)

func registerTelegramRoutes(mux *http.ServeMux, logger *slog.Logger, sc *scanner.Scanner) bool {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return false
	}
	secret := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET"))
	if secret == "" {
		logger.Error("TELEGRAM_WEBHOOK_SECRET required when TELEGRAM_BOT_TOKEN is set")
		return false
	}

	bot := telegrambot.New(
		logger,
		sc,
		telegrambot.NewClient(token, nil),
		secret,
		envOr("APP_URL", "https://app.orbitalmarkets.xyz"),
	)
	mux.Handle("POST /telegram/webhook", bot)
	logger.Info("telegram bot enabled", "webhook_path", "/telegram/webhook")
	return true
}
