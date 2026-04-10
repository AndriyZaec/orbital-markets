package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
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

const (
	// Max age before a snapshot is considered stale.
	maxSnapshotAge = 30 * time.Second
	// Minimum mark price to filter out broken/zero data.
	minMarkPrice = 1e-12
	// Hours per year for annualizing funding.
	hoursPerYear = 8760.0
)

func (s *Scanner) scan(ctx context.Context) {
	// 1. Collect snapshots from all adapters, grouped by asset.
	byAsset := s.collectByAsset(ctx)

	// 2. Pairwise comparison across venues for each asset.
	var opps []domain.Opportunity
	now := time.Now()

	for asset, snapshots := range byAsset {
		if len(snapshots) < 2 {
			continue
		}

		for i := 0; i < len(snapshots); i++ {
			for j := i + 1; j < len(snapshots); j++ {
				a := snapshots[i]
				b := snapshots[j]

				opp := s.compareSnapshots(asset, a, b, now)
				if opp == nil {
					continue
				}
				opps = append(opps, *opp)
			}
		}
	}

	// 3. Sort by absolute funding spread descending.
	sort.Slice(opps, func(i, j int) bool {
		return math.Abs(opps[i].FundingSpread) > math.Abs(opps[j].FundingSpread)
	})

	s.mu.Lock()
	s.opps = opps
	s.mu.Unlock()

	s.logger.Info("scan complete", "opportunities", len(opps), "assets_scanned", len(byAsset))
}

// collectByAsset gathers snapshots from all adapters grouped by normalized asset name.
func (s *Scanner) collectByAsset(ctx context.Context) map[string][]venue.MarketData {
	byAsset := make(map[string][]venue.MarketData)
	now := time.Now()

	for _, a := range s.adapters {
		data, err := a.FetchMarketData(ctx)
		if err != nil {
			s.logger.Error("fetch market data", "venue", a.Name(), "err", err)
			continue
		}
		for _, md := range data {
			if !isValid(md, now) {
				continue
			}
			byAsset[md.Asset] = append(byAsset[md.Asset], md)
		}
	}
	return byAsset
}

// isValid filters out snapshots that are stale or have broken data.
func isValid(md venue.MarketData, now time.Time) bool {
	if md.MarkPrice < minMarkPrice {
		return false
	}
	if md.IndexPrice < minMarkPrice {
		return false
	}
	if !md.Timestamp.IsZero() && now.Sub(md.Timestamp) > maxSnapshotAge {
		return false
	}
	return true
}

// compareSnapshots builds an opportunity from two venue snapshots of the same asset.
// Returns nil if the pair is invalid or uninteresting.
func (s *Scanner) compareSnapshots(asset string, a, b venue.MarketData, now time.Time) *domain.Opportunity {
	if a.Venue == b.Venue {
		return nil
	}

	fundingSpread := a.FundingRate - b.FundingRate

	// Direction: long the venue with lower funding, short the venue with higher funding.
	// This captures the spread (collect high funding, pay low funding).
	var direction domain.Direction
	if fundingSpread > 0 {
		// A has higher funding → short A, long B
		direction = domain.DirectionLongB
	} else {
		// B has higher funding → short B, long A
		direction = domain.DirectionLongA
	}

	// Entry spread: cost of opening both legs simultaneously.
	// Approximated as the combined bid-ask spread across venues relative to mid price.
	midA := (a.BidPrice + a.AskPrice) / 2
	midB := (b.BidPrice + b.AskPrice) / 2

	var entrySpread float64
	if midA > 0 && midB > 0 {
		spreadA := (a.AskPrice - a.BidPrice) / midA
		spreadB := (b.AskPrice - b.BidPrice) / midB
		entrySpread = spreadA + spreadB
	}

	// Annualized gross edge from funding spread (funding rates are typically per-hour).
	annualizedGross := math.Abs(fundingSpread) * hoursPerYear

	// Available notional: minimum OI across venues as a rough liquidity proxy.
	availableNotional := math.Min(a.OpenInterest, b.OpenInterest)

	// Build warnings.
	var warnings []string
	if a.BidPrice == 0 || a.AskPrice == 0 {
		warnings = append(warnings, fmt.Sprintf("%s: missing bid/ask", a.Venue))
	}
	if b.BidPrice == 0 || b.AskPrice == 0 {
		warnings = append(warnings, fmt.Sprintf("%s: missing bid/ask", b.Venue))
	}

	// Confidence based on data completeness.
	confidence := domain.ConfidenceHigh
	if len(warnings) > 0 {
		confidence = domain.ConfidenceMedium
	}
	if (a.BidPrice == 0 && a.AskPrice == 0) || (b.BidPrice == 0 && b.AskPrice == 0) {
		confidence = domain.ConfidenceLow
	}

	// Risk tier based on annualized edge magnitude.
	riskTier := classifyRisk(annualizedGross, entrySpread)

	id := fmt.Sprintf("%s-%s-%s-%s", asset, a.Venue, b.Venue, direction)

	return &domain.Opportunity{
		ID:         id,
		DetectedAt: now,
		Asset:      asset,
		VenuePair: domain.VenuePair{
			VenueA: a.Venue,
			VenueB: b.Venue,
		},
		Direction:           direction,
		FundingRateA:        a.FundingRate,
		FundingRateB:        b.FundingRate,
		FundingSpread:       fundingSpread,
		AnnualizedGrossEdge: annualizedGross,
		EntrySpreadEstimate: entrySpread,
		AvailableNotional:   availableNotional,
		Confidence:          confidence,
		RiskTier:            riskTier,
		Executable:          confidence != domain.ConfidenceLow && len(warnings) == 0,
		Warnings:            warnings,
	}
}

func classifyRisk(annualizedGross, entrySpread float64) domain.RiskTier {
	// If entry cost eats more than half the annualized edge, it's aggressive.
	if entrySpread > 0 && annualizedGross > 0 {
		ratio := entrySpread / annualizedGross
		if ratio > 0.5 {
			return domain.RiskExperimental
		}
		if ratio > 0.2 {
			return domain.RiskAggressive
		}
	}

	switch {
	case annualizedGross > 0.50: // >50% annualized
		return domain.RiskExperimental
	case annualizedGross > 0.20: // >20%
		return domain.RiskAggressive
	case annualizedGross > 0.05: // >5%
		return domain.RiskStandard
	default:
		return domain.RiskConservative
	}
}
