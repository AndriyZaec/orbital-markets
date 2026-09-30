package bracketcache

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
)

func TestCachePublishesReferenceCapabilities(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	reader := ReaderFunc(func(context.Context) (asteraccount.LeverageBrackets, time.Time, error) {
		return asteraccount.LeverageBrackets{"PIPPINUSDT": {
			{InitialLeverage: 20, NotionalFloor: 0, NotionalCap: 1_000},
			{InitialLeverage: 8, NotionalFloor: 1_000, NotionalCap: 10_000},
		}}, now, nil
	})
	cache := New(reader, discardLogger(), func() time.Time { return now })

	if capability := cache.Snapshot().Capability("PIPPINUSDT", 500); capability.Status != domain.LeverageCapabilityUnsupported {
		t.Fatalf("cold capability = %+v", capability)
	}
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	capability := cache.Snapshot().Capability("PIPPINUSDT", 1_500)
	if capability.Status != domain.LeverageCapabilityKnown || capability.Maximum == nil || *capability.Maximum != 8 ||
		capability.BracketRevision != 1 || !capability.ObservedAt.Equal(now) || !capability.ExpiresAt.Equal(now.Add(FreshnessTTL)) {
		t.Fatalf("fresh capability = %+v", capability)
	}
}

func TestCachePreservesFreshSnapshotAcrossFailureThenExpires(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	fail := false
	reader := ReaderFunc(func(context.Context) (asteraccount.LeverageBrackets, time.Time, error) {
		if fail {
			return nil, time.Time{}, errors.New("unavailable")
		}
		return asteraccount.LeverageBrackets{"AIUSDT": {{InitialLeverage: 10, NotionalFloor: 0, NotionalCap: 10_000}}}, now, nil
	})
	cache := New(reader, discardLogger(), func() time.Time { return now })
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fail = true
	now = now.Add(RefreshInterval)
	if err := cache.Refresh(context.Background()); err == nil {
		t.Fatal("refresh succeeded after source failure")
	}
	if snapshot := cache.Snapshot(); snapshot.Status != StatusDegraded || snapshot.Capability("AIUSDT", 500).Status != domain.LeverageCapabilityKnown {
		t.Fatalf("degraded snapshot = %+v", snapshot)
	}

	now = now.Add(FreshnessTTL - RefreshInterval + time.Second)
	capability := cache.Snapshot().Capability("AIUSDT", 500)
	if capability.Status != domain.LeverageCapabilityUnsupported || capability.Maximum != nil || capability.Reason != domain.LeverageReasonReferenceUnavailable {
		t.Fatalf("expired capability = %+v", capability)
	}
}

func TestCacheImmediatelyInvalidatesUnsafeReferencePermissions(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	unsafe := false
	reader := ReaderFunc(func(context.Context) (asteraccount.LeverageBrackets, time.Time, error) {
		if unsafe {
			return nil, time.Time{}, dataagent.ErrUnsafePermissions
		}
		return asteraccount.LeverageBrackets{"AIUSDT": {{InitialLeverage: 10, NotionalFloor: 0, NotionalCap: 10_000}}}, now, nil
	})
	cache := New(reader, discardLogger(), func() time.Time { return now })
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	unsafe = true
	now = now.Add(RefreshInterval)
	if err := cache.Refresh(context.Background()); !errors.Is(err, dataagent.ErrUnsafePermissions) {
		t.Fatalf("refresh error = %v, want unsafe permissions", err)
	}
	if capability := cache.Snapshot().Capability("AIUSDT", 500); capability.Status != domain.LeverageCapabilityUnsupported || capability.Maximum != nil {
		t.Fatalf("capability after permission escalation = %+v", capability)
	}
}

func TestCacheCoalescesConcurrentRefreshes(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	reader := ReaderFunc(func(context.Context) (asteraccount.LeverageBrackets, time.Time, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		close(started)
		<-release
		return asteraccount.LeverageBrackets{"BTCUSDT": {{InitialLeverage: 20, NotionalFloor: 0, NotionalCap: 10_000}}}, now, nil
	})
	cache := New(reader, discardLogger(), func() time.Time { return now })
	done := make(chan error, 2)
	go func() { done <- cache.Refresh(context.Background()) }()
	<-started
	go func() { done <- cache.Refresh(context.Background()) }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("reader calls = %d, want 1", calls)
	}
}

func TestCacheRejectsEmptyGenerationAndRecovers(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	responses := []asteraccount.LeverageBrackets{
		{},
		{"BTCUSDT": {{InitialLeverage: 20, NotionalFloor: 0, NotionalCap: 10_000}}},
	}
	reader := ReaderFunc(func(context.Context) (asteraccount.LeverageBrackets, time.Time, error) {
		response := responses[0]
		responses = responses[1:]
		return response, now, nil
	})
	cache := New(reader, discardLogger(), func() time.Time { return now })

	if err := cache.Refresh(context.Background()); err == nil {
		t.Fatal("empty generation was accepted")
	}
	if snapshot := cache.Snapshot(); snapshot.Status != StatusUnavailable || snapshot.Revision != 0 {
		t.Fatalf("failed snapshot = %+v", snapshot)
	}

	now = now.Add(RefreshInterval)
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snapshot := cache.Snapshot(); snapshot.Status != StatusReady || snapshot.Revision != 1 {
		t.Fatalf("recovered snapshot = %+v", snapshot)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
