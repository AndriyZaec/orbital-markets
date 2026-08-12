package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

var errAccountFeedCapacity = errors.New("live account feed capacity reached")

type liveAccountPosition struct {
	Symbol     string
	Side       string
	Size       float64
	EntryPrice float64
}

type liveAccountSnapshot struct {
	Venue              string
	Account            string
	Connected          bool
	LastUpdated        time.Time
	PositionsUpdatedAt time.Time
	Equity             float64
	Available          float64
	Positions          []liveAccountPosition
	LeverageBySymbol   map[string]float64
}

// liveAccountFeed hides venue-specific account state, submission, and fill tracking.
type liveAccountFeed interface {
	Snapshot() liveAccountSnapshot
	PreTradeBlockers(domain.Leg) []string
	RefreshPositions(context.Context) error
	SubmitSigned(context.Context, domain.SignedAction, *domain.SigningRequest) (*domain.SubmissionResult, error)
	WaitForFill(context.Context, *domain.SigningRequest) (*normFill, error)
	WaitForLeverage(context.Context, string, float64) error
}

// accountFeedFactory starts one account feed and must return without waiting for readiness.
type accountFeedFactory interface {
	Normalize(string) (string, error)
	Start(context.Context, string) (liveAccountFeed, error)
}

type accountFeedRegistryConfig struct {
	IdleTTL         time.Duration
	CleanupInterval time.Duration
	MaxFeeds        int
	MaxPerVenue     int
	RecoveryReserve int
	Now             func() time.Time
}

type accountFeedKey struct {
	venue   string
	account string
}

func (k accountFeedKey) String() string {
	return k.venue + ":" + k.account
}

type accountFeedEntry struct {
	key       accountFeedKey
	feed      liveAccountFeed
	cancel    context.CancelFunc
	refs      int
	lastUsed  time.Time
	operation sync.Mutex
}

type accountFeedRegistry struct {
	ctx       context.Context
	factories map[string]accountFeedFactory
	config    accountFeedRegistryConfig

	mu      sync.Mutex
	entries map[accountFeedKey]*accountFeedEntry
}

func newAccountFeedRegistry(
	ctx context.Context,
	factories map[string]accountFeedFactory,
	config accountFeedRegistryConfig,
) *accountFeedRegistry {
	if config.Now == nil {
		config.Now = time.Now
	}
	registry := &accountFeedRegistry{
		ctx: ctx, factories: factories, config: config,
		entries: make(map[accountFeedKey]*accountFeedEntry),
	}
	go registry.runCleanup()
	return registry
}

type accountFeedLease struct {
	registry *accountFeedRegistry
	entry    *accountFeedEntry
	created  bool
	once     sync.Once
}

func (l *accountFeedLease) Feed() liveAccountFeed {
	return l.entry.feed
}

func (l *accountFeedLease) Key() string {
	return l.entry.key.String()
}

func (l *accountFeedLease) Release() {
	l.release(false)
}

func (l *accountFeedLease) discardIfUnused() {
	l.release(true)
}

func (l *accountFeedLease) release(discardIfUnused bool) {
	if l == nil || l.registry == nil || l.entry == nil {
		return
	}
	l.once.Do(func() {
		l.registry.mu.Lock()
		defer l.registry.mu.Unlock()
		if current := l.registry.entries[l.entry.key]; current == l.entry {
			current.refs--
			current.lastUsed = l.registry.config.Now()
			if discardIfUnused && l.created && current.refs == 0 {
				current.cancel()
				delete(l.registry.entries, l.entry.key)
			}
		}
	})
}

func (r *accountFeedRegistry) Acquire(venue, account string) (*accountFeedLease, error) {
	return r.acquire(venue, account, false)
}

func (r *accountFeedRegistry) AcquireRecovery(venue, account string) (*accountFeedLease, error) {
	return r.acquire(venue, account, true)
}

func (r *accountFeedRegistry) acquire(venue, account string, recovery bool) (*accountFeedLease, error) {
	key, factory, err := r.normalizedKey(venue, account)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.entries[key]; entry != nil {
		entry.refs++
		entry.lastUsed = r.config.Now()
		return &accountFeedLease{registry: r, entry: entry}, nil
	}

	now := r.config.Now()
	r.cleanupIdleLocked(now)
	r.evictForCapacityLocked(key.venue, false)
	if r.atCapacityLocked(key.venue, recovery) {
		return nil, fmt.Errorf("%w for %s", errAccountFeedCapacity, key.venue)
	}

	feedCtx, cancel := context.WithCancel(r.ctx)
	feed, err := factory.Start(feedCtx, key.account)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start %s account feed: %w", key.venue, err)
	}
	entry := &accountFeedEntry{
		key: key, feed: feed, cancel: cancel,
		refs: 1, lastUsed: now,
	}
	r.entries[key] = entry
	return &accountFeedLease{registry: r, entry: entry, created: true}, nil
}

// Lookup leases an existing feed without starting one for an arbitrary read request.
func (r *accountFeedRegistry) Lookup(venue, account string) (*accountFeedLease, bool) {
	key, _, err := r.normalizedKey(venue, account)
	if err != nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[key]
	if entry == nil {
		return nil, false
	}
	entry.refs++
	entry.lastUsed = r.config.Now()
	return &accountFeedLease{registry: r, entry: entry}, true
}

func (r *accountFeedRegistry) normalizedKey(venue, account string) (accountFeedKey, accountFeedFactory, error) {
	venue = strings.ToLower(strings.TrimSpace(venue))
	factory := r.factories[venue]
	if factory == nil {
		return accountFeedKey{}, nil, fmt.Errorf("unsupported live account venue: %s", venue)
	}
	normalized, err := factory.Normalize(account)
	if err != nil {
		return accountFeedKey{}, nil, err
	}
	if normalized == "" {
		return accountFeedKey{}, nil, errors.New("live account required")
	}
	return accountFeedKey{venue: venue, account: normalized}, factory, nil
}

func (r *accountFeedRegistry) atCapacityLocked(venue string, recovery bool) bool {
	reserve := 0
	if recovery {
		reserve = r.config.RecoveryReserve
	}
	if r.config.MaxFeeds > 0 && len(r.entries) >= r.config.MaxFeeds+reserve {
		return true
	}
	if r.config.MaxPerVenue <= 0 {
		return false
	}
	count := 0
	for key := range r.entries {
		if key.venue == venue {
			count++
		}
	}
	return count >= r.config.MaxPerVenue+reserve
}

func (r *accountFeedRegistry) evictForCapacityLocked(venue string, recovery bool) {
	for r.atCapacityLocked(venue, recovery) {
		venueFull := r.config.MaxPerVenue > 0
		if venueFull {
			count := 0
			for key := range r.entries {
				if key.venue == venue {
					count++
				}
			}
			reserve := 0
			if recovery {
				reserve = r.config.RecoveryReserve
			}
			venueFull = count >= r.config.MaxPerVenue+reserve
		}
		var oldest *accountFeedEntry
		for _, entry := range r.entries {
			if entry.refs != 0 || (venueFull && entry.key.venue != venue) {
				continue
			}
			if oldest == nil || entry.lastUsed.Before(oldest.lastUsed) {
				oldest = entry
			}
		}
		if oldest == nil {
			return
		}
		oldest.cancel()
		delete(r.entries, oldest.key)
	}
}

func (r *accountFeedRegistry) runCleanup() {
	if r.config.CleanupInterval <= 0 {
		<-r.ctx.Done()
		r.closeAll()
		return
	}
	ticker := time.NewTicker(r.config.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			r.closeAll()
			return
		case <-ticker.C:
			r.cleanupIdle(r.config.Now())
		}
	}
}

func (r *accountFeedRegistry) cleanupIdle(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupIdleLocked(now)
}

func (r *accountFeedRegistry) cleanupIdleLocked(now time.Time) {
	if r.config.IdleTTL <= 0 {
		return
	}
	for key, entry := range r.entries {
		if entry.refs == 0 && now.Sub(entry.lastUsed) >= r.config.IdleTTL {
			entry.cancel()
			delete(r.entries, key)
		}
	}
}

func (r *accountFeedRegistry) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, entry := range r.entries {
		entry.cancel()
		delete(r.entries, key)
	}
}

func lockAccountFeeds(leases ...*accountFeedLease) func() {
	unique := make(map[*accountFeedEntry]struct{}, len(leases))
	entries := make([]*accountFeedEntry, 0, len(leases))
	for _, lease := range leases {
		if lease == nil || lease.entry == nil {
			continue
		}
		if _, exists := unique[lease.entry]; exists {
			continue
		}
		unique[lease.entry] = struct{}{}
		entries = append(entries, lease.entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].key.String() < entries[j].key.String()
	})
	for _, entry := range entries {
		entry.operation.Lock()
	}
	return func() {
		for i := len(entries) - 1; i >= 0; i-- {
			entries[i].operation.Unlock()
		}
	}
}
