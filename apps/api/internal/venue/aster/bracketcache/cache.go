package bracketcache

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
)

const (
	RefreshInterval = 2 * time.Minute
	FreshnessTTL    = 5 * time.Minute
)

type Status string

const (
	StatusLoading     Status = "loading"
	StatusReady       Status = "ready"
	StatusDegraded    Status = "degraded"
	StatusStale       Status = "stale"
	StatusUnavailable Status = "unavailable"
)

type Reader interface {
	ReadAllLeverageBrackets(context.Context) (asteraccount.LeverageBrackets, time.Time, error)
}

type ReaderFunc func(context.Context) (asteraccount.LeverageBrackets, time.Time, error)

func (f ReaderFunc) ReadAllLeverageBrackets(ctx context.Context) (asteraccount.LeverageBrackets, time.Time, error) {
	return f(ctx)
}

type Snapshot struct {
	Status     Status
	ObservedAt time.Time
	ExpiresAt  time.Time
	Revision   uint64
	brackets   asteraccount.LeverageBrackets
	now        time.Time
}

func (s Snapshot) Capability(symbol string, notional float64) domain.LeverageCapability {
	capability := domain.LeverageCapability{
		RequestedNotional: notional,
		BracketRevision:   s.Revision,
		ObservedAt:        s.ObservedAt,
		ExpiresAt:         s.ExpiresAt,
	}
	if s.Revision == 0 {
		capability.Status = domain.LeverageCapabilityUnsupported
		capability.Reason = domain.LeverageReasonReferenceUnavailable
		return capability
	}
	if !s.ExpiresAt.IsZero() && s.now.After(s.ExpiresAt) {
		capability.Status = domain.LeverageCapabilityUnsupported
		capability.Reason = domain.LeverageReasonReferenceUnavailable
		return capability
	}
	if math.IsNaN(notional) || math.IsInf(notional, 0) || notional < 0 {
		capability.Status = domain.LeverageCapabilityOutOfRange
		capability.Reason = domain.LeverageReasonInvalidNotional
		return capability
	}
	if len(s.brackets[symbol]) == 0 {
		capability.Status = domain.LeverageCapabilityMissing
		capability.Reason = domain.LeverageReasonBracketMissing
		return capability
	}
	maximum, found := asteraccount.MaximumLeverage(s.brackets, symbol, notional)
	if !found {
		capability.Status = domain.LeverageCapabilityOutOfRange
		capability.Reason = domain.LeverageReasonNotionalOutOfRange
		return capability
	}
	capability.Status = domain.LeverageCapabilityKnown
	capability.Maximum = &maximum
	return capability
}

type refresh struct {
	done chan struct{}
	err  error
}

type Cache struct {
	reader Reader
	logger *slog.Logger
	now    func() time.Time

	mu          sync.RWMutex
	snapshot    Snapshot
	lastAttempt time.Time
	inflight    *refresh
}

func New(reader Reader, logger *slog.Logger, now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	return &Cache{reader: reader, logger: logger, now: now, snapshot: Snapshot{Status: StatusLoading}}
}

func (c *Cache) Snapshot() Snapshot {
	now := c.now().UTC()
	c.mu.RLock()
	snapshot := c.snapshot
	c.mu.RUnlock()
	snapshot.now = now
	if snapshot.Revision > 0 && now.After(snapshot.ExpiresAt) {
		snapshot.Status = StatusStale
	}
	return snapshot
}

func (c *Cache) Refresh(ctx context.Context) error {
	now := c.now().UTC()
	c.mu.Lock()
	if c.inflight != nil {
		current := c.inflight
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-current.done:
			return current.err
		}
	}
	if !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < RefreshInterval {
		c.mu.Unlock()
		return nil
	}
	current := &refresh{done: make(chan struct{})}
	c.inflight = current
	c.lastAttempt = now
	c.mu.Unlock()

	brackets, observedAt, err := c.reader.ReadAllLeverageBrackets(ctx)
	if err == nil && (observedAt.IsZero() || len(brackets) == 0) {
		err = errors.New("Aster reference brackets were empty")
	}

	c.mu.Lock()
	if err == nil {
		c.snapshot = Snapshot{
			Status: StatusReady, ObservedAt: observedAt.UTC(), ExpiresAt: observedAt.UTC().Add(FreshnessTTL),
			Revision: c.snapshot.Revision + 1, brackets: copyBrackets(brackets),
		}
		c.logger.Info("Aster reference brackets refreshed", "symbols", len(brackets), "revision", c.snapshot.Revision)
	} else if errors.Is(err, dataagent.ErrUnsafePermissions) && c.snapshot.Revision > 0 {
		c.snapshot.Status = StatusStale
		c.snapshot.ExpiresAt = now.Add(-time.Nanosecond)
		c.logger.Warn("Aster reference bracket permissions became unsafe", "status", c.snapshot.Status)
	} else if c.snapshot.Revision == 0 {
		c.snapshot.Status = StatusUnavailable
		c.logger.Warn("Aster reference brackets unavailable", "err", err)
	} else {
		c.snapshot.Status = StatusDegraded
		if now.After(c.snapshot.ExpiresAt) {
			c.snapshot.Status = StatusStale
		}
		c.logger.Warn("Aster reference bracket refresh failed", "status", c.snapshot.Status, "err", err)
	}
	current.err = err
	close(current.done)
	c.inflight = nil
	c.mu.Unlock()
	return err
}

func (c *Cache) Run(ctx context.Context) {
	_ = c.Refresh(ctx)
	ticker := time.NewTicker(RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.Refresh(ctx)
		}
	}
}

func copyBrackets(source asteraccount.LeverageBrackets) asteraccount.LeverageBrackets {
	result := make(asteraccount.LeverageBrackets, len(source))
	for symbol, brackets := range source {
		result[symbol] = append([]asteraccount.LeverageBracket(nil), brackets...)
	}
	return result
}
