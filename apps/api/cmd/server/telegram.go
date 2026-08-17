package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/telegrambot"
)

func withTelegramBot(logger *slog.Logger, sc *scanner.Scanner, fallback http.Handler) http.Handler {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return fallback
	}
	secret := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET"))
	if secret == "" {
		logger.Error("TELEGRAM_WEBHOOK_SECRET required when TELEGRAM_BOT_TOKEN is set")
		return fallback
	}

	bot := telegrambot.New(
		logger,
		sc,
		telegrambot.NewClient(token, nil),
		secret,
		envOr("APP_URL", "https://app.orbitalmarkets.xyz"),
	)
	rootMux := http.NewServeMux()
	rootMux.Handle("POST /telegram/webhook", bot)
	rootMux.Handle("/", fallback)
	logger.Info("telegram bot enabled", "webhook_path", "/telegram/webhook")
	return rootMux
}
