package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
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
	pac *pacifica.Adapter,
	hl *hyperliquid.Adapter,
) *api.LiveDeps {
	logger.Info("live execution: starting runtime")

	hlAssetMap := hl.AssetMap()

	// --- Live position store + monitor ---
	liveStore := executor.NewStore(database, logger)
	signingStore := domain.NewSigningRequestStore()
	liveDeps := api.NewLiveDeps(ctx, logger, signingStore, liveStore, hlAssetMap, pac)
	liveMonitor := executor.NewMonitor(logger, liveStore, market, liveDeps)
	fundingMonitor := executor.NewFundingMonitor(logger, liveStore, map[string]venue.FundingHistory{
		"pacifica":    pac,
		"hyperliquid": hl,
	})
	go liveMonitor.Run(ctx)
	go fundingMonitor.Run(ctx)

	logger.Info("live execution: runtime ready (account streams start on wallet connect)")
	return liveDeps
}
