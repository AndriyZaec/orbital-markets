package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type fakeAccountFeed struct {
	snapshot liveAccountSnapshot
}

func (f *fakeAccountFeed) Snapshot() liveAccountSnapshot        { return f.snapshot }
func (f *fakeAccountFeed) PreTradeBlockers(domain.Leg) []string { return nil }
func (f *fakeAccountFeed) SubmitSigned(context.Context, domain.SignedAction, *domain.SigningRequest) (*domain.SubmissionResult, error) {
	return nil, nil
}
func (f *fakeAccountFeed) WaitForFill(context.Context, *domain.SigningRequest) (*normFill, error) {
	return nil, nil
}

type fakeAccountFeedFactory struct {
	starts  atomic.Int64
	stops   atomic.Int64
	started chan struct{}
}

func (f *fakeAccountFeedFactory) Normalize(account string) (string, error) {
	account = strings.ToLower(strings.TrimSpace(account))
	if account == "" {
		return "", errors.New("account required")
	}
	return account, nil
}

func (f *fakeAccountFeedFactory) Start(ctx context.Context, account string) (liveAccountFeed, error) {
	f.starts.Add(1)
	if f.started != nil {
		f.started <- struct{}{}
	}
	go func() {
		<-ctx.Done()
		f.stops.Add(1)
	}()
	return &fakeAccountFeed{snapshot: liveAccountSnapshot{Account: account}}, nil
}

func TestAccountFeedRegistryDeduplicatesConcurrentAcquire(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory := &fakeAccountFeedFactory{}
	registry := newTestAccountFeedRegistry(ctx, factory, accountFeedRegistryConfig{})

	const callers = 20
	var wg sync.WaitGroup
	leases := make(chan *accountFeedLease, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := registry.Acquire("venue", " Account-A ")
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			leases <- lease
		}()
	}
	wg.Wait()
	close(leases)
	for lease := range leases {
		lease.Release()
	}
	if starts := factory.starts.Load(); starts != 1 {
		t.Fatalf("feed starts = %d, want 1", starts)
	}
}

func TestAccountFeedRegistryIsolatesAccounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := newTestAccountFeedRegistry(ctx, &fakeAccountFeedFactory{}, accountFeedRegistryConfig{})

	first, err := registry.Acquire("venue", "account-a")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := registry.Acquire("venue", "account-b")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if first.Feed() == second.Feed() {
		t.Fatal("different accounts shared one feed")
	}
	if got := first.Feed().Snapshot().Account; got != "account-a" {
		t.Fatalf("first account = %q", got)
	}
	if got := second.Feed().Snapshot().Account; got != "account-b" {
		t.Fatalf("second account = %q", got)
	}
}

func TestAccountFeedRegistryEvictsOnlyIdleFeeds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	factory := &fakeAccountFeedFactory{}
	registry := newTestAccountFeedRegistry(ctx, factory, accountFeedRegistryConfig{
		IdleTTL: time.Minute,
		Now:     func() time.Time { return now },
	})

	active, err := registry.Acquire("venue", "active")
	if err != nil {
		t.Fatal(err)
	}
	idle, err := registry.Acquire("venue", "idle")
	if err != nil {
		t.Fatal(err)
	}
	idle.Release()
	now = now.Add(2 * time.Minute)
	registry.cleanupIdle(now)

	if _, exists := registry.Lookup("venue", "idle"); exists {
		t.Fatal("idle feed was not evicted")
	}
	activeAgain, exists := registry.Lookup("venue", "active")
	if !exists {
		t.Fatal("leased feed was evicted")
	}
	activeAgain.Release()
	active.Release()
}

func TestAccountFeedRegistryRejectsCapacityUntilIdleEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	registry := newTestAccountFeedRegistry(ctx, &fakeAccountFeedFactory{}, accountFeedRegistryConfig{
		IdleTTL:     time.Minute,
		MaxFeeds:    1,
		MaxPerVenue: 1,
		Now:         func() time.Time { return now },
	})

	first, err := registry.Acquire("venue", "account-a")
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	if _, err := registry.Acquire("venue", "account-b"); !errors.Is(err, errAccountFeedCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
	now = now.Add(2 * time.Minute)
	second, err := registry.Acquire("venue", "account-b")
	if err != nil {
		t.Fatalf("acquire after idle eviction: %v", err)
	}
	second.Release()
}

func newTestAccountFeedRegistry(
	ctx context.Context,
	factory accountFeedFactory,
	config accountFeedRegistryConfig,
) *accountFeedRegistry {
	return newAccountFeedRegistry(ctx, map[string]accountFeedFactory{"venue": factory}, config)
}
