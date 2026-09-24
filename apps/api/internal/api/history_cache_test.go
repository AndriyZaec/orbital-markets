package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHistoryCacheCoalescesConcurrentMisses(t *testing.T) {
	cache := newHistoryCache(8)
	key := historyCacheKey{asset: "SOL", venueA: "a", venueB: "b", range_: "7d"}
	started := make(chan struct{})
	release := make(chan struct{})
	var loads atomic.Int32
	load := func(context.Context) (historyResponse, error) {
		if loads.Add(1) == 1 {
			close(started)
		}
		<-release
		return historyResponse{Asset: "SOL"}, nil
	}

	const callers = 8
	results := make(chan historyCacheResult, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, result, err := cache.get(context.Background(), context.Background(), key, time.Minute, load)
			if err != nil {
				t.Errorf("get: %v", err)
			}
			results <- result
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)

	if got := loads.Load(); got != 1 {
		t.Fatalf("loads = %d, want 1", got)
	}
	misses := 0
	for result := range results {
		if result == historyCacheMiss {
			misses++
		}
	}
	if misses != 1 {
		t.Fatalf("misses = %d, want 1", misses)
	}
}

func TestHistoryCacheServesStaleWhileRefreshing(t *testing.T) {
	cache := newHistoryCache(8)
	key := historyCacheKey{asset: "SOL", venueA: "a", venueB: "b", range_: "7d"}
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var loads atomic.Int32
	load := func(context.Context) (historyResponse, error) {
		loadNumber := loads.Add(1)
		if loadNumber == 2 {
			close(refreshStarted)
			<-releaseRefresh
		}
		return historyResponse{Asset: string(rune('0' + loadNumber))}, nil
	}

	first, result, err := cache.get(context.Background(), context.Background(), key, time.Millisecond, load)
	if err != nil || result != historyCacheMiss || first.Asset != "1" {
		t.Fatalf("first = %#v, %q, %v", first, result, err)
	}
	time.Sleep(2 * time.Millisecond)

	stale, result, err := cache.get(context.Background(), context.Background(), key, time.Minute, load)
	if err != nil || result != historyCacheStale || stale.Asset != "1" {
		t.Fatalf("stale = %#v, %q, %v", stale, result, err)
	}
	<-refreshStarted
	close(releaseRefresh)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		fresh, result, err := cache.get(context.Background(), context.Background(), key, time.Minute, load)
		if err == nil && result == historyCacheHit && fresh.Asset == "2" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("refreshed response was not published")
}

func TestHistoryCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := newHistoryCache(1)
	load := func(asset string) func(context.Context) (historyResponse, error) {
		return func(context.Context) (historyResponse, error) {
			return historyResponse{Asset: asset}, nil
		}
	}
	keyA := historyCacheKey{asset: "SOL"}
	keyB := historyCacheKey{asset: "BTC"}
	_, _, _ = cache.get(context.Background(), context.Background(), keyA, time.Minute, load("SOL"))
	_, _, _ = cache.get(context.Background(), context.Background(), keyB, time.Minute, load("BTC"))

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(cache.entries))
	}
	if _, ok := cache.entries[keyB]; !ok {
		t.Fatal("newest entry was evicted")
	}
}

func TestHistoryCachePreservesStaleResponseAfterRefreshFailure(t *testing.T) {
	cache := newHistoryCache(8)
	key := historyCacheKey{asset: "SOL"}
	var fail atomic.Bool
	load := func(context.Context) (historyResponse, error) {
		if fail.Load() {
			return historyResponse{}, errors.New("database unavailable")
		}
		return historyResponse{Asset: "SOL"}, nil
	}
	_, _, _ = cache.get(context.Background(), context.Background(), key, time.Millisecond, load)
	time.Sleep(2 * time.Millisecond)
	fail.Store(true)

	stale, result, err := cache.get(context.Background(), context.Background(), key, time.Minute, load)
	if err != nil || result != historyCacheStale || stale.Asset != "SOL" {
		t.Fatalf("stale = %#v, %q, %v", stale, result, err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cache.mu.Lock()
		_, refreshing := cache.flights[key]
		entry := cache.entries[key]
		cache.mu.Unlock()
		if !refreshing && !entry.retryAt.IsZero() {
			preserved, result, err := cache.get(context.Background(), context.Background(), key, time.Minute, load)
			if err != nil || result != historyCacheStale || preserved.Asset != "SOL" {
				t.Fatalf("preserved = %#v, %q, %v", preserved, result, err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("failed refresh did not enter retry backoff")
}
