package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid"
	hlaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/account"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

// startLive creates and starts the live execution runtime.
// Returns nil if required config is missing — the server starts without live endpoints.
//
// Required env:
//   - PACIFICA_ACCOUNT — Solana pubkey for Pacifica account state + order tracking
//   - HL_WS_ADDRESS   — Ethereum address for Hyperliquid WS subscriptions (orderUpdates, userFills)
//
// These identify which accounts to monitor for pre-trade state and fill tracking.
// Signing happens on the frontend. The backend never holds private keys.
func startLive(
	ctx context.Context,
	logger *slog.Logger,
	database *sql.DB,
	market executor.MarketSource,
	pac *pacifica.Adapter,
	hl *hyperliquid.Adapter,
) *api.LiveDeps {
	pacAccount := os.Getenv("PACIFICA_ACCOUNT")
	hlWSAddress := os.Getenv("HL_WS_ADDRESS")

	if pacAccount == "" || hlWSAddress == "" {
		var missing []string
		if pacAccount == "" {
			missing = append(missing, "PACIFICA_ACCOUNT")
		}
		if hlWSAddress == "" {
			missing = append(missing, "HL_WS_ADDRESS")
		}
		logger.Warn("live execution disabled: missing env vars", "missing", missing)
		return nil
	}

	logger.Info("live execution: starting runtime")

	// --- Pacifica ---

	// Account state
	pacState := pacaccount.NewAccountState()

	// Order/fill tracker (also serves as StreamHandler for private WS)
	pacTracker := paclive.NewTracker(logger)

	// Account subscriber — feeds state + forwards order/trade updates to tracker.
	// Uses account address (no API key needed — Pacifica account subs are by pubkey).
	pacSub := pacaccount.NewSubscriber(logger, pacState, pacAccount, pacTracker)
	go pacSub.Run(ctx)
	logger.Info("live execution: pacifica account subscriber started", "account", pacAccount)

	// Live client — nil signer (non-custodial mode).
	// Only SubmitSignedOrder is usable; custodial SubmitMarketOrder will error.
	pacClient := paclive.NewClient(logger, nil, pacState)
	logger.Info("live execution: pacifica live client ready (non-custodial)")

	// --- Hyperliquid ---

	// Account state + subscriber (REST polling — HL has no WS for account state)
	hlState := hlaccount.NewAccountState()
	hlAcctSub := hlaccount.NewSubscriber(logger, hlState, hlWSAddress)
	go hlAcctSub.Run(ctx)
	logger.Info("live execution: hyperliquid account subscriber started", "address", hlWSAddress)

	// Order/fill tracker — needs the operator's address to subscribe to WS streams
	hlTracker := hllive.NewTracker(logger, hlWSAddress)
	go hlTracker.Run(ctx)
	logger.Info("live execution: hyperliquid order tracker started", "address", hlWSAddress)

	// Asset map — populated by the market data adapter on each poll cycle
	hlAssetMap := hl.AssetMap()
	logger.Info("live execution: hyperliquid asset map wired (populated on next poll)")

	// Live client — nil signer (non-custodial mode)
	hlClient := hllive.NewClient(logger, nil, hlAssetMap, hlState, hlTracker)
	logger.Info("live execution: hyperliquid live client ready (non-custodial)")

	// --- Live position store + monitor ---
	liveStore := executor.NewStore(database, logger)
	liveMonitor := executor.NewMonitor(logger, liveStore, market)
	go liveMonitor.Run(ctx)
	logger.Info("live execution: live position monitor started")

	// --- Signing store ---
	signingStore := domain.NewSigningRequestStore()
	logger.Info("live execution: signing request store ready")

	logger.Info("live execution: runtime started — live endpoints enabled")

	return api.NewLiveDeps(signingStore, liveStore, pacClient, pacTracker, hlClient, hlAssetMap)
}
