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

// Service has no background market collection. Only selected opportunities
// request public REST books; bounded single-flight caches share those reads.
type Service struct {
	mu     sync.Mutex
	ctx    context.Context
	logger *slog.Logger
	client *http.Client
	urls   map[string]string
	pairs  map[Pair]*flight[Estimate]
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
		pairs: make(map[Pair]*flight[Estimate]), books: make(map[Market]*flight[book]), quotas: make(map[string]*quota),
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

func (s *Service) Estimate(ctx context.Context, pair Pair) (Estimate, error) {
	if err := ctx.Err(); err != nil {
		return Estimate{}, err
	}
	if pair.Long.Venue == pair.Short.Venue || pair.Long.Key == "" || pair.Short.Key == "" {
		return Estimate{}, ErrUnavailable
	}
	if s.urls[pair.Long.Venue] == "" || s.urls[pair.Short.Venue] == "" {
		return Estimate{}, ErrUnavailable
	}
	s.mu.Lock()
	entry := s.pairs[pair]
	if entry != nil && !entry.finished.IsZero() && time.Since(entry.finished) > 15*time.Second {
		delete(s.pairs, pair)
		entry = nil
	}
	if entry == nil {
		if !makeRoom(s.pairs, 128) {
			s.mu.Unlock()
			return Estimate{}, ErrUnavailable
		}
		entry = &flight[Estimate]{done: make(chan struct{})}
		s.pairs[pair] = entry
		go s.estimate(pair, entry)
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return Estimate{}, ctx.Err()
	case <-entry.done:
		return entry.value, entry.err
	}
}

func (s *Service) estimate(pair Pair, entry *flight[Estimate]) {
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	defer cancel()
	type result struct {
		book book
		err  error
	}
	longResult := make(chan result, 1)
	go func() { b, err := s.getBook(ctx, pair.Long); longResult <- result{b, err} }()
	short, err := s.getBook(ctx, pair.Short)
	long := <-longResult
	value := Estimate{}
	if err == nil {
		err = long.err
	}
	if err == nil {
		var ok bool
		value, ok = capacity(long.book, short, time.Now())
		if !ok {
			err = ErrUnavailable
		}
	}
	if err != nil {
		s.logger.Debug("selected opportunity L2 unavailable", "long_venue", pair.Long.Venue, "short_venue", pair.Short.Venue, "err", err)
	}
	s.mu.Lock()
	entry.value, entry.err, entry.finished = value, err, time.Now()
	close(entry.done)
	s.mu.Unlock()
}

// Quote bypasses the display estimate cache. Fresh execution checks also bypass
// completed book entries, but still share in-flight reads and venue budgets.
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
