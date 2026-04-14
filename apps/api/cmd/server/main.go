package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/paper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pac := pacifica.New(logger)
	hl := hyperliquid.New(logger)

	sc := scanner.New(logger, pac, hl)

	// Paper trading
	store := paper.NewStore()
	executor := paper.NewExecutor(logger, store, sc)
	monitor := paper.NewMonitor(logger, executor, store, sc)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	go pac.Connect(ctx)
	go hl.Run(ctx)
	go sc.Run(ctx, 30*time.Second)
	go monitor.Run(ctx)

	srv := api.NewServer(ctx, logger, sc, executor, store)

	addr := envOr("ADDR", ":8080")
	logger.Info("starting server", "addr", addr)

	httpSrv := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		httpSrv.Shutdown(context.Background())
	}()

	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
