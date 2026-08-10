package api

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacificlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

const (
	defaultAccountFeedIdleTTL         = 10 * time.Minute
	defaultAccountFeedCleanupInterval = time.Minute
	defaultMaxAccountFeeds            = 100
	defaultMaxAccountFeedsPerVenue    = 50
	defaultRecoveryAccountFeedReserve = 10
)

// LiveDeps holds dependencies for live non-custodial execution. Account feeds
// are started lazily and shared by normalized venue+account key.
type LiveDeps struct {
	signingStore        *domain.SigningRequestStore
	liveStore           *executor.Store
	sessions            *SessionManager
	accounts            *accountFeedRegistry
	hlAssetMap          hllive.AssetMap
	hlAgentApprover     hyperliquidAgentApprover
	pacificaAgentBinder pacificaAgentBinder
}

func NewLiveDeps(
	ctx context.Context,
	logger *slog.Logger,
	signingStore *domain.SigningRequestStore,
	liveStore *executor.Store,
	hlAssetMap hllive.AssetMap,
) *LiveDeps {
	factories := map[string]accountFeedFactory{
		"pacifica":    &pacificaAccountFeedFactory{logger: logger},
		"hyperliquid": &hyperliquidAccountFeedFactory{logger: logger, assetMap: hlAssetMap},
	}
	return &LiveDeps{
		signingStore:        signingStore,
		liveStore:           liveStore,
		sessions:            NewSessionManager(),
		hlAssetMap:          hlAssetMap,
		hlAgentApprover:     hllive.NewDefaultAgentApprover(),
		pacificaAgentBinder: pacificlive.NewDefaultAgentBinder(),
		accounts: newAccountFeedRegistry(ctx, factories, accountFeedRegistryConfig{
			IdleTTL:         defaultAccountFeedIdleTTL,
			CleanupInterval: defaultAccountFeedCleanupInterval,
			MaxFeeds:        defaultMaxAccountFeeds,
			MaxPerVenue:     defaultMaxAccountFeedsPerVenue,
			RecoveryReserve: defaultRecoveryAccountFeedReserve,
		}),
	}
}

type liveAccountContext struct {
	leases map[string]*accountFeedLease
}

func (d *LiveDeps) acquireAccounts(pacificaAccount, hyperliquidAccount string) (*liveAccountContext, error) {
	return d.acquireAccountContext(map[string]string{
		"pacifica": pacificaAccount, "hyperliquid": hyperliquidAccount,
	}, false)
}

func (d *LiveDeps) acquireRecoveryAccounts(pacificaAccount, hyperliquidAccount string) (*liveAccountContext, error) {
	return d.acquireAccountContext(map[string]string{
		"pacifica": pacificaAccount, "hyperliquid": hyperliquidAccount,
	}, true)
}

func (d *LiveDeps) acquireAccountContext(accounts map[string]string, recovery bool) (*liveAccountContext, error) {
	if d == nil || d.accounts == nil {
		return nil, fmt.Errorf("live account registry not configured")
	}
	venues := make([]string, 0, len(accounts))
	for venue := range accounts {
		venues = append(venues, venue)
	}
	sort.Strings(venues)
	accountContext := &liveAccountContext{leases: make(map[string]*accountFeedLease, len(venues))}
	for _, venue := range venues {
		var lease *accountFeedLease
		var err error
		if recovery {
			lease, err = d.accounts.AcquireRecovery(venue, accounts[venue])
		} else {
			lease, err = d.accounts.Acquire(venue, accounts[venue])
		}
		if err != nil {
			for _, acquired := range accountContext.leases {
				acquired.discardIfUnused()
			}
			return nil, err
		}
		accountContext.leases[venue] = lease
	}
	return accountContext, nil
}

func (c *liveAccountContext) Feed(venue string) (liveAccountFeed, bool) {
	if c == nil {
		return nil, false
	}
	lease := c.leases[venue]
	if lease == nil {
		return nil, false
	}
	return lease.Feed(), true
}

func (c *liveAccountContext) Lock() func() {
	if c == nil {
		return func() {}
	}
	leases := make([]*accountFeedLease, 0, len(c.leases))
	for _, lease := range c.leases {
		leases = append(leases, lease)
	}
	return lockAccountFeeds(leases...)
}

func (c *liveAccountContext) Release() {
	if c == nil {
		return
	}
	for _, lease := range c.leases {
		lease.Release()
	}
}

func accountForVenue(venue, pacificaAccount, hyperliquidAccount string) string {
	switch venue {
	case "pacifica":
		return pacificaAccount
	case "hyperliquid":
		return hyperliquidAccount
	default:
		return ""
	}
}

func (d *LiveDeps) validateSigningAccount(request *domain.SigningRequest, signer string) error {
	if request == nil || request.Account == "" {
		return fmt.Errorf("signing request account missing")
	}
	expectedSigner := request.Signer
	if expectedSigner == "" {
		expectedSigner = request.Account
	}
	expected, _, err := d.accounts.normalizedKey(request.Venue, expectedSigner)
	if err != nil {
		return err
	}
	actual, _, err := d.accounts.normalizedKey(request.Venue, signer)
	if err != nil {
		return err
	}
	if expected.account != actual.account {
		return fmt.Errorf("signer does not match prepared %s signer", request.Venue)
	}
	return nil
}
