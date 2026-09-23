package main

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
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
	ast *aster.Adapter,
	asterReader dataagent.Reader,
) *api.LiveDeps {
	logger.Info("live execution: starting runtime")

	hlAssetMap := hl.AssetMap()

	// --- Live position store + monitor ---
	liveStore := executor.NewStore(database, logger)
	signingStore := domain.NewSigningRequestStore()
	liveDeps := api.NewLiveDeps(ctx, logger, signingStore, liveStore, hlAssetMap, pac, ast, asterReader)
	liveMonitor := executor.NewMonitor(logger, liveStore, market, liveDeps)
	fundingSources := map[string]venue.FundingHistory{
		"pacifica":    pac,
		"hyperliquid": hl,
	}
	if asterReader != nil {
		fundingSources["aster"] = asterFundingHistory{reader: asterReader}
	}
	fundingMonitor := executor.NewFundingMonitor(logger, liveStore, fundingSources)
	go liveMonitor.Run(ctx)
	go fundingMonitor.Run(ctx)

	logger.Info("live execution: runtime ready (account streams start on wallet connect)")
	return liveDeps
}

type asterFundingHistory struct {
	reader dataagent.Reader
}

var _ venue.FundingHistory = asterFundingHistory{}

func (history asterFundingHistory) FundingPayments(
	ctx context.Context,
	account, _ string,
	since, until time.Time,
) ([]venue.FundingPayment, error) {
	return history.reader.ReadFunding(ctx, account, since, until)
}
