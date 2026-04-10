package scanner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type Scanner struct {
	adapters []venue.Adapter
	mu       sync.RWMutex
	opps     []domain.Opportunity
	logger   *slog.Logger
}

func New(logger *slog.Logger, adapters ...venue.Adapter) *Scanner {
	return &Scanner{
		adapters: adapters,
		logger:   logger,
	}
}

func (s *Scanner) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *Scanner) Opportunities() []domain.Opportunity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Opportunity, len(s.opps))
	copy(out, s.opps)
	return out
}

// MarketData returns the latest snapshots from all adapters.
func (s *Scanner) MarketData(ctx context.Context) []venue.MarketData {
	var all []venue.MarketData
	for _, a := range s.adapters {
		data, err := a.FetchMarketData(ctx)
		if err != nil {
			s.logger.Error("fetch market data", "venue", a.Name(), "err", err)
			continue
		}
		all = append(all, data...)
	}
	return all
}

func (s *Scanner) scan(ctx context.Context) {
	s.logger.Info("scanning venues", "adapters", len(s.adapters))
	// TODO: fetch from adapters, compute spreads, rank opportunities
}
