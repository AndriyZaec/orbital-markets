package api

import (
	"context"
	"log/slog"
	"sync"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	hlaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/account"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

// LiveDeps holds dependencies for live (non-custodial) execution.
// Venue clients and stores are created eagerly at startup. Account subscribers
// start lazily via EnsureAccountStreams when wallets connect.
type LiveDeps struct {
	signingStore *domain.SigningRequestStore
	liveStore    *executor.Store
	sessions     *SessionManager
	pacClient    *paclive.Client
	pacTracker   *paclive.Tracker
	pacState     *pacaccount.AccountState
	hlState      *hlaccount.AccountState
	hlClient     *hllive.Client
	hlAssetMap   hllive.AssetMap

	ctx    context.Context
	logger *slog.Logger
	mu     sync.Mutex
	active bool
}

// NewLiveDeps creates a LiveDeps. Venue clients are created eagerly; account
// subscribers start lazily via EnsureAccountStreams when wallets connect.
func NewLiveDeps(
	ctx context.Context,
	logger *slog.Logger,
	signingStore *domain.SigningRequestStore,
	liveStore *executor.Store,
	pacClient *paclive.Client,
	pacTracker *paclive.Tracker,
	pacState *pacaccount.AccountState,
	hlState *hlaccount.AccountState,
	hlAssetMap hllive.AssetMap,
) *LiveDeps {
	return &LiveDeps{
		ctx:          ctx,
		logger:       logger,
		signingStore: signingStore,
		liveStore:    liveStore,
		sessions:     NewSessionManager(),
		pacClient:    pacClient,
		pacTracker:   pacTracker,
		pacState:     pacState,
		hlState:      hlState,
		hlAssetMap:   hlAssetMap,
	}
}

// EnsureAccountStreams starts venue account subscribers if not already running.
// Called on the first /live/prepare with the connected wallet addresses.
// Safe to call multiple times — only the first call starts the subscribers.
func (d *LiveDeps) EnsureAccountStreams(pacAccount, hlAddress string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active {
		return
	}

	// Pacifica account subscriber
	pacSub := pacaccount.NewSubscriber(d.logger, d.pacState, pacAccount, d.pacTracker)
	go pacSub.Run(d.ctx)
	d.logger.Info("live: pacifica account subscriber started", "account", pacAccount)

	// Hyperliquid account subscriber (REST polling)
	hlAcctSub := hlaccount.NewSubscriber(d.logger, d.hlState, hlAddress)
	go hlAcctSub.Run(d.ctx)
	d.logger.Info("live: hyperliquid account subscriber started", "address", hlAddress)

	// Hyperliquid order/fill tracker (WS)
	hlTracker := hllive.NewTracker(d.logger, hlAddress)
	go hlTracker.Run(d.ctx)
	d.logger.Info("live: hyperliquid order tracker started", "address", hlAddress)

	// Wire the HL client now that we have a tracker
	d.hlClient = hllive.NewClient(d.logger, nil, d.hlAssetMap, d.hlState, hlTracker)
	d.logger.Info("live: hyperliquid live client ready")

	d.active = true
}
