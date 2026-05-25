package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid"
	hlaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/account"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

// startLive creates and starts the live execution runtime.
// Returns nil if required config is missing — the server starts without live endpoints.
func startLive(
	ctx context.Context,
	logger *slog.Logger,
	pac *pacifica.Adapter,
	hl *hyperliquid.Adapter,
) *api.LiveDeps {
	pacAPIKey := os.Getenv("PACIFICA_API_KEY")
	pacAccount := os.Getenv("PACIFICA_ACCOUNT")
	hlAddress := os.Getenv("HL_ADDRESS")

	if pacAPIKey == "" || pacAccount == "" || hlAddress == "" {
		var missing []string
		if pacAPIKey == "" {
			missing = append(missing, "PACIFICA_API_KEY")
		}
		if pacAccount == "" {
			missing = append(missing, "PACIFICA_ACCOUNT")
		}
		if hlAddress == "" {
			missing = append(missing, "HL_ADDRESS")
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

	// Account subscriber — feeds state + forwards order/trade updates to tracker
	pacSub := pacaccount.NewSubscriber(logger, pacState, pacAPIKey, pacTracker)
	go pacSub.Run(ctx)
	logger.Info("live execution: pacifica account subscriber started")

	// Live client (non-custodial — signer identifies the account but signing
	// happens on the frontend via the prepare/submit API flow)
	pacSigner := &nonCustodialPacificaSigner{account: pacAccount}
	pacClient := paclive.NewClient(logger, pacSigner, pacState)
	logger.Info("live execution: pacifica live client ready", "account", pacAccount)

	// --- Hyperliquid ---

	// Account state
	hlState := hlaccount.NewAccountState()
	// No HL account subscriber exists yet — state stays disconnected until one is built.
	// The pre-trade validator will block submissions when state is not connected,
	// which is the correct behavior.
	logger.Warn("live execution: hyperliquid account subscriber not yet implemented — account state will show disconnected")

	// Order/fill tracker
	hlTracker := hllive.NewTracker(logger, hlAddress)
	go hlTracker.Run(ctx)
	logger.Info("live execution: hyperliquid order tracker started", "address", hlAddress)

	// Asset map — populated by the market data adapter on each poll cycle
	hlAssetMap := hl.AssetMap()
	logger.Info("live execution: hyperliquid asset map wired (populated on next poll)")

	// Live client (non-custodial)
	hlSigner := &nonCustodialHyperliquidSigner{address: hlAddress}
	hlClient := hllive.NewClient(logger, hlSigner, hlAssetMap, hlState, hlTracker)
	logger.Info("live execution: hyperliquid live client ready", "address", hlAddress)

	// --- Signing store ---
	signingStore := domain.NewSigningRequestStore()
	logger.Info("live execution: signing request store ready")

	logger.Info("live execution: runtime started — live endpoints enabled")

	return api.NewLiveDeps(signingStore, pacClient, pacTracker, hlClient, hlAssetMap)
}

// nonCustodialPacificaSigner identifies the Pacifica account but refuses to sign.
// In the non-custodial flow, signing happens on the frontend via the
// prepare/submit API. The existing Client.SubmitMarketOrder path (which calls
// Sign) must not be used — only Client.SubmitSignedOrder is valid.
type nonCustodialPacificaSigner struct {
	account string
}

func (s *nonCustodialPacificaSigner) Account() string { return s.account }
func (s *nonCustodialPacificaSigner) Sign([]byte) (string, error) {
	return "", fmt.Errorf("non-custodial mode: signing must happen on the frontend via /api/v1/live/prepare + /api/v1/live/submit")
}

// nonCustodialHyperliquidSigner identifies the Hyperliquid address but refuses to sign.
type nonCustodialHyperliquidSigner struct {
	address string
}

func (s *nonCustodialHyperliquidSigner) Address() string { return s.address }
func (s *nonCustodialHyperliquidSigner) SignAction(any, int64) (string, error) {
	return "", fmt.Errorf("non-custodial mode: signing must happen on the frontend via /api/v1/live/prepare + /api/v1/live/submit")
}
