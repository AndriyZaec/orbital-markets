package main

import (
	"log/slog"
	"net/http"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

func buildRootHandler(logger *slog.Logger, sc *scanner.Scanner, apiHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	if !registerTelegramRoutes(mux, logger, sc) {
		return apiHandler
	}
	mux.Handle("/", apiHandler)
	return mux
}
