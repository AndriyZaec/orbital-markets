package api

import (
	"context"
	"sync"
	"time"
)

type historyCacheKey struct {
	asset  string
	venueA string
	venueB string
	range_ string
}

type historyCacheEntry struct {
	response   historyResponse
	expiresAt  time.Time
	retryAt    time.Time
	lastAccess time.Time
}

type historyCacheFlight struct {
	done     chan struct{}
	response historyResponse
	err      error
}

type historyCacheResult string

const (
	historyCacheHit       historyCacheResult = "hit"
	historyCacheStale     historyCacheResult = "stale"
	historyCacheMiss      historyCacheResult = "miss"
	historyCacheCoalesced historyCacheResult = "coalesced"
)

type historyCache struct {
	mu         sync.Mutex
	entries    map[historyCacheKey]historyCacheEntry
	flights    map[historyCacheKey]*historyCacheFlight
	maxEntries int
}

func newHistoryCache(maxEntries int) *historyCache {
	return &historyCache{
		entries:    make(map[historyCacheKey]historyCacheEntry),
		flights:    make(map[historyCacheKey]*historyCacheFlight),
		maxEntries: maxEntries,
	}
}

func (c *historyCache) get(
	ctx context.Context,
	refreshCtx context.Context,
	key historyCacheKey,
	ttl time.Duration,
	load func(context.Context) (historyResponse, error),
) (historyResponse, historyCacheResult, error) {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		entry.lastAccess = now
		c.entries[key] = entry
		if now.Before(entry.expiresAt) {
			c.mu.Unlock()
			return entry.response, historyCacheHit, nil
		}
		if _, running := c.flights[key]; !running && !now.Before(entry.retryAt) {
			flight := &historyCacheFlight{done: make(chan struct{})}
			c.flights[key] = flight
			go c.refresh(refreshCtx, key, ttl, flight, load)
		}
		c.mu.Unlock()
		return entry.response, historyCacheStale, nil
	}

	if flight, running := c.flights[key]; running {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return historyResponse{}, historyCacheCoalesced, ctx.Err()
		case <-flight.done:
			return flight.response, historyCacheCoalesced, flight.err
		}
	}

	flight := &historyCacheFlight{done: make(chan struct{})}
	c.flights[key] = flight
	c.mu.Unlock()

	go c.refresh(refreshCtx, key, ttl, flight, load)
	select {
	case <-ctx.Done():
		return historyResponse{}, historyCacheMiss, ctx.Err()
	case <-flight.done:
		return flight.response, historyCacheMiss, flight.err
	}
}

func (c *historyCache) refresh(ctx context.Context, key historyCacheKey, ttl time.Duration, flight *historyCacheFlight, load func(context.Context) (historyResponse, error)) {
	response, err := load(ctx)
	c.finish(key, ttl, flight, response, err)
}

func (c *historyCache) finish(key historyCacheKey, ttl time.Duration, flight *historyCacheFlight, response historyResponse, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		now := time.Now()
		c.entries[key] = historyCacheEntry{response: response, expiresAt: now.Add(ttl), lastAccess: now}
		c.evictOldest()
	} else if entry, ok := c.entries[key]; ok {
		retryDelay := min(ttl, 15*time.Second)
		entry.retryAt = time.Now().Add(retryDelay)
		c.entries[key] = entry
	}
	flight.response = response
	flight.err = err
	delete(c.flights, key)
	close(flight.done)
}

func (c *historyCache) evictOldest() {
	for len(c.entries) > c.maxEntries {
		var oldestKey historyCacheKey
		var oldest time.Time
		for key, entry := range c.entries {
			if oldest.IsZero() || entry.lastAccess.Before(oldest) {
				oldestKey = key
				oldest = entry.lastAccess
			}
		}
		delete(c.entries, oldestKey)
	}
}
