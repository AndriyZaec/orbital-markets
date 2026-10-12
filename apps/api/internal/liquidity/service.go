package liquidity

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("L2 estimate unavailable")

type Market struct{ Venue, Key string }
type Pair struct{ Long, Short Market }
type flight[T any] struct {
	done     chan struct{}
	value    T
	err      error
	finished time.Time
}

type quota struct {
	requests    []time.Time
	pausedUntil time.Time
	slot        chan struct{}
}

// Service has no background market collection. Only orders needing more than
// fresh BBO depth request public books; bounded single-flight caches share reads.
type Service struct {
	mu     sync.Mutex
	ctx    context.Context
	logger *slog.Logger
	client *http.Client
	urls   map[string]string
	books  map[Market]*flight[book]
	quotas map[string]*quota
}

func New(ctx context.Context, logger *slog.Logger) *Service {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost, transport.MaxIdleConnsPerHost = 2, 2
	transport.IdleConnTimeout = 30 * time.Second
	s := &Service{
		ctx: ctx, logger: logger, client: &http.Client{Transport: transport, Timeout: 2 * time.Second},
		urls:  map[string]string{"aster": "https://fapi.asterdex.com", "hyperliquid": "https://api.hyperliquid.xyz", "pacifica": "https://api.pacifica.fi"},
		books: make(map[Market]*flight[book]), quotas: make(map[string]*quota),
	}
	for name := range s.urls {
		s.quotas[name] = &quota{slot: make(chan struct{}, 1)}
	}
	return s
}

func makeRoom[K comparable, V any](cache map[K]*flight[V], limit int) bool {
	if len(cache) < limit {
		return true
	}
	var oldest K
	var at time.Time
	for key, entry := range cache {
		if !entry.finished.IsZero() && (at.IsZero() || entry.finished.Before(at)) {
			oldest, at = key, entry.finished
		}
	}
	if at.IsZero() {
		return false
	}
	delete(cache, oldest)
	return true
}

// Fresh execution checks bypass completed book entries, but still share
// in-flight reads and venue budgets.
func (s *Service) Quote(ctx context.Context, pair Pair, size OrderSize, fresh bool) (OrderQuote, error) {
	if err := ctx.Err(); err != nil {
		return OrderQuote{}, err
	}
	if pair.Long.Venue == pair.Short.Venue || pair.Long.Key == "" || pair.Short.Key == "" || s.urls[pair.Long.Venue] == "" || s.urls[pair.Short.Venue] == "" {
		return OrderQuote{}, ErrUnavailable
	}
	if !((finitePositive(size.Notional) && size.BaseAmount == 0) || (finitePositive(size.BaseAmount) && size.Notional == 0)) {
		return OrderQuote{}, ErrUnavailable
	}
	if fresh {
		s.mu.Lock()
		for _, market := range []Market{pair.Long, pair.Short} {
			if entry := s.books[market]; entry != nil && !entry.finished.IsZero() {
				delete(s.books, market)
			}
		}
		s.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	type result struct {
		book book
		err  error
	}
	longResult := make(chan result, 1)
	go func() { b, err := s.getBook(ctx, pair.Long); longResult <- result{b, err} }()
	short, err := s.getBook(ctx, pair.Short)
	long := <-longResult
	if err != nil {
		return OrderQuote{}, err
	}
	if long.err != nil {
		return OrderQuote{}, long.err
	}
	if err := ctx.Err(); err != nil {
		return OrderQuote{}, err
	}
	amount := size.BaseAmount
	if size.Notional > 0 {
		if !validBook(long.book, time.Now()) || !validBook(short, time.Now()) {
			return OrderQuote{}, ErrUnavailable
		}
		amount = size.Notional / max(long.book.asks[0].price, short.bids[0].price)
	}
	return quoteOrder(long.book, short, amount, time.Now())
}

func (s *Service) getBook(ctx context.Context, market Market) (book, error) {
	s.mu.Lock()
	entry := s.books[market]
	if entry != nil && !entry.finished.IsZero() && time.Since(entry.finished) > time.Second {
		delete(s.books, market)
		entry = nil
	}
	if entry == nil {
		if !makeRoom(s.books, 256) {
			s.mu.Unlock()
			return book{}, ErrUnavailable
		}
		entry = &flight[book]{done: make(chan struct{})}
		s.books[market] = entry
		// A caller cancelling its view must not cancel a shared market read for
		// other views. The process context and short deadline bound that work.
		go func() {
			readCtx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
			defer cancel()
			value, err := s.fetchBook(readCtx, market)
			if err != nil {
				s.logger.Debug("execution L2 book unavailable", "venue", market.Venue, "market", market.Key, "err", err)
			}
			s.mu.Lock()
			entry.value, entry.err, entry.finished = value, err, time.Now()
			close(entry.done)
			s.mu.Unlock()
		}()
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return book{}, ctx.Err()
	case <-entry.done:
		return entry.value, entry.err
	}
}

func (s *Service) allow(name string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.quotas[name]
	if now.Before(q.pausedUntil) {
		return false
	}
	cutoff := now.Add(-time.Minute)
	for len(q.requests) > 0 && !q.requests[0].After(cutoff) {
		q.requests = q.requests[1:]
	}
	limit := 100 // HL: 200 weight/min; Aster depth50: 200 weight/min
	if name == "pacifica" {
		limit = 30
	}
	if len(q.requests) >= limit {
		return false
	}
	q.requests = append(q.requests, now)
	return true
}

func (s *Service) pause(name string, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	until := time.Now().Add(duration)
	if until.After(s.quotas[name].pausedUntil) {
		s.quotas[name].pausedUntil = until
	}
}
