package main

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/telegrambot"
)

type telegramIntegration struct {
	bot   *telegrambot.Bot
	links *telegrambot.LinkService
}

func buildTelegramIntegration(
	logger *slog.Logger,
	sc *scanner.Scanner,
	database *sql.DB,
) *telegramIntegration {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		return nil
	}
	secret := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET"))
	if secret == "" {
		logger.Error("TELEGRAM_WEBHOOK_SECRET required when TELEGRAM_BOT_TOKEN is set")
		return nil
	}

	var links *telegrambot.LinkService
	var options []telegrambot.Option
	if username := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_USERNAME")); username != "" {
		links = telegrambot.NewLinkService(database, username)
		options = append(options,
			telegrambot.WithAccountLinks(links),
			telegrambot.WithPositions(executor.NewStore(database, logger)),
		)
	} else {
		logger.Warn("Telegram account linking disabled: TELEGRAM_BOT_USERNAME is not set")
	}
	bot := telegrambot.New(
		logger,
		sc,
		telegrambot.NewClient(token, nil),
		secret,
		envOr("APP_URL", "https://app.orbitalmarkets.xyz"),
		options...,
	)
	logger.Info("telegram bot enabled", "webhook_path", "/telegram/webhook")
	return &telegramIntegration{bot: bot, links: links}
}

func (t *telegramIntegration) registerRoutes(mux *http.ServeMux) bool {
	if t == nil || t.bot == nil {
		return false
	}
	mux.Handle("POST /telegram/webhook", t.bot)
	return true
}
