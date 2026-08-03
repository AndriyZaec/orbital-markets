package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica"
)

// startLive creates the shared registry and stores eagerly. Venue account
// feeds start lazily on connect/prepare and are reused by normalized account.
func startLive(
	ctx context.Context,
	logger *slog.Logger,
	database *sql.DB,
	market executor.MarketSource,
	_ *pacifica.Adapter,
	hl *hyperliquid.Adapter,
) *api.LiveDeps {
	logger.Info("live execution: starting runtime")

	hlAssetMap := hl.AssetMap()

	// --- Live position store + monitor ---
	liveStore := executor.NewStore(database, logger)
	liveMonitor := executor.NewMonitor(logger, liveStore, market)
	go liveMonitor.Run(ctx)

	signingStore := domain.NewSigningRequestStore()

	logger.Info("live execution: runtime ready (account streams start on wallet connect)")

	return api.NewLiveDeps(
		ctx, logger,
		signingStore, liveStore,
		hlAssetMap,
	)
}
