package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/mr-tron/base58"

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
	agentAuthorizations *agentAuthorizationRegistry
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
		agentAuthorizations: newAgentAuthorizationRegistry(liveStore),
		accounts: newAccountFeedRegistry(ctx, factories, accountFeedRegistryConfig{
			IdleTTL:         defaultAccountFeedIdleTTL,
			CleanupInterval: defaultAccountFeedCleanupInterval,
			MaxFeeds:        defaultMaxAccountFeeds,
			MaxPerVenue:     defaultMaxAccountFeedsPerVenue,
			RecoveryReserve: defaultRecoveryAccountFeedReserve,
		}),
	}
}

type agentAuthorizationRegistry struct {
	store *executor.Store
}

func newAgentAuthorizationRegistry(store *executor.Store) *agentAuthorizationRegistry {
	return &agentAuthorizationRegistry{store: store}
}

func normalizeAgentAuthorization(venue, value string) string {
	value = strings.TrimSpace(value)
	if venue == "hyperliquid" {
		value = strings.ToLower(value)
	}
	return value
}

func (r *agentAuthorizationRegistry) record(ctx context.Context, venue, owner, agent string) error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.UpsertAgentAuthorization(
		ctx, venue, normalizeAgentAuthorization(venue, owner), normalizeAgentAuthorization(venue, agent),
	)
}

func (r *agentAuthorizationRegistry) matches(ctx context.Context, venue, owner, agent string) (bool, error) {
	if r == nil || r.store == nil {
		return true, nil
	}
	return r.store.AgentAuthorizationMatches(
		ctx, venue, normalizeAgentAuthorization(venue, owner), normalizeAgentAuthorization(venue, agent),
	)
}

func (d *LiveDeps) recordAgentAuthorization(ctx context.Context, venue, owner, agent string) error {
	if d.agentAuthorizations == nil {
		return nil
	}
	return d.agentAuthorizations.record(ctx, venue, owner, agent)
}

func (d *LiveDeps) agentAuthorizationMatches(ctx context.Context, venue, owner, agent string) (bool, error) {
	if d.agentAuthorizations == nil {
		return true, nil
	}
	return d.agentAuthorizations.matches(ctx, venue, owner, agent)
}

func (d *LiveDeps) agentPairAuthorizationMatches(
	ctx context.Context,
	accountPacifica, accountHyperliquid, agentPacifica, agentHyperliquid string,
) (bool, error) {
	pacificaMatches, err := d.agentAuthorizationMatches(ctx, "pacifica", accountPacifica, agentPacifica)
	if err != nil || !pacificaMatches {
		return pacificaMatches, err
	}
	return d.agentAuthorizationMatches(ctx, "hyperliquid", accountHyperliquid, agentHyperliquid)
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

func (d *LiveDeps) lockAgentOwner(venue, owner string) (func(), error) {
	if d == nil || d.accounts == nil {
		return func() {}, nil
	}
	lease, err := d.accounts.AcquireRecovery(venue, owner)
	if err != nil {
		return nil, err
	}
	unlockFeed := lockAccountFeeds(lease)
	return func() {
		unlockFeed()
		lease.Release()
	}, nil
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

func signerForVenue(venue, pacificaAgent, hyperliquidAgent string) string {
	switch venue {
	case "pacifica":
		return pacificaAgent
	case "hyperliquid":
		return hyperliquidAgent
	default:
		return ""
	}
}

func (d *LiveDeps) validateAgentIdentity(venue, owner, agent string) error {
	switch venue {
	case "pacifica":
		decoded, err := base58.Decode(strings.TrimSpace(agent))
		if err != nil || len(decoded) != 32 {
			return fmt.Errorf("invalid pacifica agent address")
		}
	case "hyperliquid":
		normalized := strings.TrimSpace(agent)
		if len(normalized) != 42 || !strings.HasPrefix(normalized, "0x") {
			return fmt.Errorf("invalid hyperliquid agent address")
		}
		if _, err := hex.DecodeString(normalized[2:]); err != nil {
			return fmt.Errorf("invalid hyperliquid agent address")
		}
	}
	ownerKey, _, err := d.accounts.normalizedKey(venue, owner)
	if err != nil {
		return err
	}
	agentKey, _, err := d.accounts.normalizedKey(venue, agent)
	if err != nil {
		return err
	}
	if ownerKey.account == agentKey.account {
		return fmt.Errorf("%s agent must differ from owner account", venue)
	}
	return nil
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
