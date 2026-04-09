package scanner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type VenueAdapter interface {
	Name() string
	FetchFundingRates(ctx context.Context) (map[string]float64, error)
	FetchMarkPrices(ctx context.Context) (map[string]float64, error)
}

type Scanner struct {
	adapters []VenueAdapter
	mu       sync.RWMutex
	opps     []domain.Opportunity
	logger   *slog.Logger
}

func New(logger *slog.Logger, adapters ...VenueAdapter) *Scanner {
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

func (s *Scanner) scan(ctx context.Context) {
	s.logger.Info("scanning venues", "adapters", len(s.adapters))
	// TODO: fetch from adapters, compute spreads, rank opportunities
}
