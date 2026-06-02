package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid"
	hlaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

// startLive creates the live execution runtime eagerly.
// Venue clients and stores are always created. Account subscribers (which need
// wallet addresses) start lazily on the first /live/prepare call, using the
// addresses from the connected wallets. No env vars required.
func startLive(
	ctx context.Context,
	logger *slog.Logger,
	database *sql.DB,
	market executor.MarketSource,
	_ *pacifica.Adapter,
	hl *hyperliquid.Adapter,
) *api.LiveDeps {
	logger.Info("live execution: starting runtime")

	// --- Pacifica (eager — no address needed for client/tracker) ---
	pacState := pacaccount.NewAccountState()
	pacTracker := paclive.NewTracker(logger)
	pacClient := paclive.NewClient(logger, nil, pacState)

	// --- Hyperliquid (eager — client needs asset map, not address) ---
	hlState := hlaccount.NewAccountState()
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
		pacClient, pacTracker, pacState,
		hlState, hlAssetMap,
	)
}
