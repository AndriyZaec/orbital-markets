package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mr-tron/base58"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
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
	logger                        *slog.Logger
	signingStore                  *domain.SigningRequestStore
	liveStore                     *executor.Store
	sessions                      *SessionManager
	accounts                      *accountFeedRegistry
	modules                       *venue.LiveModuleRegistry
	hlBuilder                     *hllive.BuilderCode
	asterBuilder                  *asterlive.BuilderConfig
	asterPrivate                  asterPrivateSubmitter
	asterAgentApprover            asterAgentApprover
	hlAgentApprover               hyperliquidAgentApprover
	hlBuilderApprover             hyperliquidBuilderApprover
	pacificaAgentBinder           pacificaAgentBinder
	pacificaAgentRevoker          pacificaAgentRevoker
	pacificaBuilder               *pacificlive.BuilderConfig
	pacificaBuilderApprover       pacificaBuilderCodeApprover
	pacificaBuilderApprovalReader pacificaBuilderCodeApprovalReader
	agentAuthorizations           *agentAuthorizationRegistry
	agentOwnerLocks               [64]sync.Mutex
}

type accountLeverageSource interface {
	LeverageCapability(context.Context, string, float64, bool) domain.LeverageCapability
}

func (d *LiveDeps) accountLeverageResolver(accounts map[string]string) scanner.LeverageCapResolver {
	if d == nil || d.accounts == nil {
		return func(_ context.Context, venueName, _ string, notional float64, _ bool) domain.LeverageCapability {
			if venueName != "aster" {
				return domain.LeverageCapability{Status: domain.LeverageCapabilityUnsupported, RequestedNotional: notional, Reason: domain.LeverageReasonVenueUnsupported}
			}
			return domain.LeverageCapability{Status: domain.LeverageCapabilityPending, RequestedNotional: notional, Reason: domain.LeverageReasonAccountPending}
		}
	}
	return func(ctx context.Context, venueName, symbol string, notional float64, refresh bool) domain.LeverageCapability {
		if venueName != "aster" {
			return domain.LeverageCapability{Status: domain.LeverageCapabilityUnsupported, RequestedNotional: notional, Reason: domain.LeverageReasonVenueUnsupported}
		}
		account := accounts[venueName]
		if account == "" {
			return domain.LeverageCapability{Status: domain.LeverageCapabilityPending, RequestedNotional: notional, Reason: domain.LeverageReasonAccountPending}
		}
		var lease *accountFeedLease
		var found bool
		if refresh {
			lease, found = d.accounts.Lookup(venueName, account)
		} else {
			lease, found = d.accounts.LookupPassive(venueName, account)
		}
		if !found && refresh {
			var err error
			lease, err = d.accounts.Acquire(venueName, account)
			found = err == nil
		}
		if !found {
			return domain.LeverageCapability{Status: domain.LeverageCapabilityPending, RequestedNotional: notional, Reason: domain.LeverageReasonAccountPending}
		}
		defer lease.Release()
		source, ok := lease.Feed().(accountLeverageSource)
		if !ok {
			return domain.LeverageCapability{Status: domain.LeverageCapabilityUnsupported, RequestedNotional: notional, Reason: domain.LeverageReasonVenueUnsupported}
		}
		capability := source.LeverageCapability(ctx, symbol, notional, refresh)
		if d.logger != nil {
			d.logger.Info("account leverage capability",
				"venue", venueName,
				"symbol", symbol,
				"status", capability.Status,
				"reason", capability.Reason,
			)
		}
		return capability
	}
}

func NewLiveDeps(
	ctx context.Context,
	logger *slog.Logger,
	signingStore *domain.SigningRequestStore,
	liveStore *executor.Store,
	hlAssetMap hllive.AssetMap,
	pacificaLotSizes pacificlive.LotSizeMap,
	asterRules asterlive.OrderRuleMap,
	asterReader dataagent.Reader,
) *LiveDeps {
	asterClient := asterlive.NewDefaultClient(logger)
	hlBuilder := hllive.OrbitalBuilderCode()
	asterBuilder := &asterlive.BuilderConfig{
		Address: hlBuilder.Address,
		FeeRate: strconv.FormatFloat(float64(hlBuilder.Fee)/100_000, 'f', -1, 64),
	}
	modules, err := venue.NewLiveModuleRegistry(
		pacificlive.NewLiveModule(pacificaLotSizes),
		hllive.NewLiveModule(hlAssetMap),
		asterlive.NewLiveModule(asterRules, asterBuilder),
	)
	if err != nil {
		panic(fmt.Sprintf("configure live venue modules: %v", err))
	}
	hlApprover := hllive.NewDefaultAgentApprover()
	pacificaBuilderApprover := pacificlive.NewDefaultBuilderCodeApprover()
	deps := &LiveDeps{
		logger:                        logger,
		signingStore:                  signingStore,
		liveStore:                     liveStore,
		sessions:                      NewSessionManager(),
		modules:                       modules,
		hlBuilder:                     hlBuilder,
		asterBuilder:                  asterBuilder,
		asterPrivate:                  asterClient,
		asterAgentApprover:            asterlive.NewDefaultAgentApprover(),
		hlAgentApprover:               hlApprover,
		hlBuilderApprover:             hlApprover,
		pacificaAgentBinder:           pacificlive.NewDefaultAgentBinder(),
		pacificaAgentRevoker:          pacificlive.NewDefaultAgentRevoker(),
		pacificaBuilder:               pacificlive.OrbitalBuilderConfig(),
		pacificaBuilderApprover:       pacificaBuilderApprover,
		pacificaBuilderApprovalReader: pacificaBuilderApprover,
		agentAuthorizations:           newAgentAuthorizationRegistry(liveStore),
	}
	factories := map[string]accountFeedFactory{
		"aster": &asterAccountFeedFactory{
			client: asterClient, reader: asterReader, fundingReader: asterReader, orderReader: asterReader,
			applyFunding: deps.applyAsterFundingBatch, logger: logger, backendReads: true,
		},
		"pacifica":    &pacificaAccountFeedFactory{logger: logger},
		"hyperliquid": &hyperliquidAccountFeedFactory{logger: logger, assetMap: hlAssetMap},
	}
	deps.accounts = newAccountFeedRegistry(ctx, factories, accountFeedRegistryConfig{
		IdleTTL:         defaultAccountFeedIdleTTL,
		CleanupInterval: defaultAccountFeedCleanupInterval,
		MaxFeeds:        defaultMaxAccountFeeds,
		MaxPerVenue:     defaultMaxAccountFeedsPerVenue,
		RecoveryReserve: defaultRecoveryAccountFeedReserve,
	})
	return deps
}

func (d *LiveDeps) liveModule(name string) (venue.LiveModule, error) {
	if d == nil || d.modules == nil {
		return nil, fmt.Errorf("live venue modules not configured")
	}
	module, ok := d.modules.Module(name)
	if !ok {
		return nil, fmt.Errorf("unsupported venue: %s", name)
	}
	return module, nil
}

func (d *LiveDeps) applyAsterPrivateResult(
	ctx context.Context,
	request *domain.SigningRequest,
	result *asterlive.PrivateResult,
) (bool, error) {
	if result != nil && result.FundingPayments != nil {
		return true, d.applyAsterFundingBatch(
			ctx, request.Account, result.FundingPayments, result.SubmittedAt, result.RespondedAt,
		)
	}
	if result == nil || (result.AccountUpdate == nil && !result.DepositRequired) {
		return false, nil
	}
	if d == nil || d.accounts == nil {
		return false, fmt.Errorf("Aster account registry unavailable")
	}
	lease, found := d.accounts.Lookup("aster", request.Account)
	if !found {
		return false, fmt.Errorf("Aster account feed unavailable")
	}
	defer lease.Release()
	feed, ok := lease.Feed().(*asterAccountFeed)
	if !ok {
		return false, fmt.Errorf("invalid Aster account feed")
	}
	return feed.ApplyPrivateResult(request, result)
}

func (d *LiveDeps) applyAsterFundingBatch(
	ctx context.Context,
	account string,
	payments []venue.FundingPayment,
	_ time.Time,
	_ time.Time,
) error {
	if d == nil || d.liveStore == nil {
		return fmt.Errorf("live position store unavailable")
	}
	positions, err := d.liveStore.ListFundingPositionsByVenueAccount(ctx, "aster", account)
	if err != nil {
		return err
	}
	paymentsByPosition := make(map[string][]venue.FundingPayment)
	for _, payment := range payments {
		matches := make([]string, 0, 1)
		for i := range positions {
			position := &positions[i]
			openedAt, parseErr := time.Parse(time.RFC3339, position.OpenedAt)
			if parseErr != nil || payment.PaidAt.Before(openedAt) {
				continue
			}
			if position.CompletedAt != "" {
				completedAt, parseErr := time.Parse(time.RFC3339, position.CompletedAt)
				if parseErr != nil || payment.PaidAt.After(completedAt) {
					continue
				}
			}
			fills, fillErr := d.liveStore.GetFills(ctx, position.ID)
			if fillErr != nil {
				return fillErr
			}
			for _, fill := range fills {
				if fill.Venue == "aster" && fill.Filled && fill.Symbol == payment.MarketKey {
					matches = append(matches, position.ID)
					break
				}
			}
		}
		if len(matches) == 1 {
			paymentsByPosition[matches[0]] = append(paymentsByPosition[matches[0]], payment)
		} else if len(matches) > 1 {
			return fmt.Errorf("Aster funding payment %s matches multiple positions", payment.ExternalID)
		}
	}
	for i := range positions {
		position := &positions[i]
		openedAt, parseErr := time.Parse(time.RFC3339, position.OpenedAt)
		if parseErr != nil {
			continue
		}
		if err := d.liveStore.ApplyObservedFunding(
			ctx,
			position.ID,
			"aster",
			account,
			position.Asset,
			openedAt,
			paymentsByPosition[position.ID],
		); err != nil {
			return err
		}
	}
	return nil
}

type agentAuthorizationRegistry struct {
	store *executor.Store
}

func newAgentAuthorizationRegistry(store *executor.Store) *agentAuthorizationRegistry {
	return &agentAuthorizationRegistry{store: store}
}

func normalizeAgentAuthorization(venue, value string) string {
	value = strings.TrimSpace(value)
	if venue == "hyperliquid" || venue == "aster" {
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

func (r *agentAuthorizationRegistry) remove(ctx context.Context, venue, owner, agent string) error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.DeleteAgentAuthorization(
		ctx, venue, normalizeAgentAuthorization(venue, owner), normalizeAgentAuthorization(venue, agent),
	)
}

func (d *LiveDeps) recordAgentAuthorization(ctx context.Context, venue, owner, agent string) error {
	if d.agentAuthorizations == nil {
		return nil
	}
	return d.agentAuthorizations.record(ctx, venue, owner, agent)
}

func (d *LiveDeps) removeAgentAuthorization(ctx context.Context, venue, owner, agent string) error {
	if d.agentAuthorizations == nil {
		return nil
	}
	return d.agentAuthorizations.remove(ctx, venue, owner, agent)
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
	return d.agentAuthorizationsMatch(ctx, map[string]string{
		"pacifica": accountPacifica, "hyperliquid": accountHyperliquid,
	}, map[string]string{
		"pacifica": agentPacifica, "hyperliquid": agentHyperliquid,
	})
}

func (d *LiveDeps) agentAuthorizationsMatch(
	ctx context.Context,
	accounts, agents map[string]string,
) (bool, error) {
	venues := make([]string, 0, len(accounts))
	for venue := range accounts {
		venues = append(venues, venue)
	}
	sort.Strings(venues)
	for _, venue := range venues {
		matches, err := d.agentAuthorizationMatches(ctx, venue, accounts[venue], agents[venue])
		if err != nil || !matches {
			return matches, err
		}
	}
	return true, nil
}

type liveAccountContext struct {
	leases map[string]*accountFeedLease
}

func (c *liveAccountContext) mutationGeneration() map[string]uint64 {
	generation := make(map[string]uint64, len(c.leases))
	for venue, lease := range c.leases {
		generation[venue] = lease.entry.mutations.Load()
	}
	return generation
}

func (c *liveAccountContext) mutatedSince(generation map[string]uint64) bool {
	for venue, lease := range c.leases {
		if lease.entry.mutations.Load() != generation[venue] {
			return true
		}
	}
	return false
}

func (c *liveAccountContext) markMutation(venue string) {
	if lease := c.leases[venue]; lease != nil {
		lease.markMutation()
	}
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
	if _, supported := d.accounts.factories[venue]; !supported {
		hasher := fnv.New32a()
		_, _ = hasher.Write([]byte(venue + ":" + normalizeAgentAuthorization(venue, owner)))
		lock := &d.agentOwnerLocks[hasher.Sum32()%uint32(len(d.agentOwnerLocks))]
		lock.Lock()
		return lock.Unlock, nil
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
