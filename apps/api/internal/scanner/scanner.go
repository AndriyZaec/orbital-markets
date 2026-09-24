package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type Scanner struct {
	adapters        []venue.Adapter
	scanMu          sync.Mutex
	mu              sync.RWMutex
	opps            []domain.Opportunity
	generation      uint64
	sourceRevisions map[string]uint64
	logger          *slog.Logger
}

func New(logger *slog.Logger, adapters ...venue.Adapter) *Scanner {
	return &Scanner{
		adapters:        adapters,
		sourceRevisions: make(map[string]uint64, len(adapters)),
		logger:          logger,
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
	for i := range s.opps {
		out[i] = cloneOpportunity(s.opps[i])
	}
	return out
}

// MarketData returns the latest snapshots from all adapters.
func (s *Scanner) MarketData(ctx context.Context) []venue.MarketData {
	var all []venue.MarketData
	now := time.Now()
	for _, a := range s.adapters {
		data, err := a.FetchMarketData(ctx)
		if err != nil {
			s.logger.Error("fetch market data", "venue", a.Name(), "err", err)
			continue
		}
		for _, snapshot := range data {
			if isValid(snapshot, now) {
				all = append(all, snapshot)
			}
		}
	}
	return all
}

// MarketSnapshot returns the latest cached snapshot for one venue and asset.
func (s *Scanner) MarketSnapshot(ctx context.Context, venueName, asset string) (venue.MarketData, error) {
	for _, adapter := range s.adapters {
		if !strings.EqualFold(adapter.Name(), venueName) {
			continue
		}
		data, err := adapter.FetchMarketData(ctx)
		if err != nil {
			return venue.MarketData{}, fmt.Errorf("fetch %s market data: %w", venueName, err)
		}
		for _, snapshot := range data {
			if strings.EqualFold(snapshot.Asset, asset) && isValid(snapshot, time.Now()) {
				return snapshot, nil
			}
		}
		return venue.MarketData{}, fmt.Errorf("%s market data unavailable for %s", venueName, asset)
	}
	return venue.MarketData{}, fmt.Errorf("unsupported venue: %s", venueName)
}

const (
	// Max age before a snapshot is considered stale.
	maxSnapshotAge = 30 * time.Second
	// Minimum mark price to filter out broken/zero data.
	minMarkPrice = 1e-12
)

func (s *Scanner) scan(ctx context.Context) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	// 1. Collect snapshots from all adapters, grouped by asset.
	collection := s.collectByAsset(ctx)
	byAsset := collection.byAsset

	s.mu.RLock()
	previous := make([]domain.Opportunity, len(s.opps))
	copy(previous, s.opps)
	generation := s.generation + 1
	s.mu.RUnlock()
	previousByID := make(map[string]domain.Opportunity, len(previous))
	for _, opportunity := range previous {
		previousByID[opportunity.ID] = opportunity
	}

	// 2. Pairwise comparison across venues for each asset.
	var opps []domain.Opportunity
	currentByID := make(map[string]struct{})
	now := time.Now()

	for asset, snapshots := range byAsset {
		if len(snapshots) < 2 {
			continue
		}

		for i := 0; i < len(snapshots); i++ {
			for j := i + 1; j < len(snapshots); j++ {
				a, b := canonicalVenuePair(snapshots[i], snapshots[j])

				opp := s.compareSnapshots(asset, a, b, now)
				if opp == nil {
					continue
				}
				opp.Status = domain.OpportunityAvailable
				opp.Generation = generation
				opp.SourceRevisions = pairSourceRevisions(s.sourceRevisions, a.Venue, b.Venue)
				if old, found := previousByID[opp.ID]; found {
					opp.DetectedAt = old.DetectedAt
				}
				opps = append(opps, *opp)
				currentByID[opp.ID] = struct{}{}
			}
		}
	}
	for _, old := range previous {
		if _, found := currentByID[old.ID]; found {
			continue
		}
		old.Status = nextUnavailableStatus(old.Status)
		old.Generation = generation
		old.SourceRevisions = pairSourceRevisions(s.sourceRevisions, old.VenuePair.VenueA, old.VenuePair.VenueB)
		old.AvailabilityReasons = collection.unavailableReasons(old)
		old.ExecutionStatus = "blocked"
		opps = append(opps, old)
	}

	// 3. Preserve spread position across health transitions so limited API
	// responses do not flicker a row solely because its source degraded.
	sort.Slice(opps, func(i, j int) bool {
		return math.Abs(opps[i].FundingSpread) > math.Abs(opps[j].FundingSpread)
	})

	s.mu.Lock()
	s.opps = opps
	s.generation = generation
	s.mu.Unlock()

	available, degraded, unavailable := opportunityStatusCounts(opps)
	s.logger.Info("scan complete",
		"generation", generation,
		"opportunities", len(opps),
		"available", available,
		"degraded", degraded,
		"unavailable", unavailable,
		"assets_scanned", len(byAsset),
	)
}

type marketCollection struct {
	byAsset       map[string][]venue.MarketData
	assetsByVenue map[string]map[string]struct{}
	fetchFailed   map[string]bool
}

// collectByAsset gathers snapshots from all adapters grouped by normalized asset name.
func (s *Scanner) collectByAsset(ctx context.Context) marketCollection {
	collection := marketCollection{
		byAsset:       make(map[string][]venue.MarketData),
		assetsByVenue: make(map[string]map[string]struct{}, len(s.adapters)),
		fetchFailed:   make(map[string]bool),
	}
	now := time.Now()

	for _, a := range s.adapters {
		venueName := a.Name()
		data, err := a.FetchMarketData(ctx)
		if err != nil {
			collection.fetchFailed[venueName] = true
			s.logger.Error("fetch market data", "venue", venueName, "err", err)
			continue
		}
		s.sourceRevisions[venueName]++
		collection.assetsByVenue[venueName] = make(map[string]struct{})
		for _, md := range data {
			if !isValid(md, now) {
				continue
			}
			collection.byAsset[md.Asset] = append(collection.byAsset[md.Asset], md)
			collection.assetsByVenue[venueName][md.Asset] = struct{}{}
		}
	}
	return collection
}

func (c marketCollection) unavailableReasons(opportunity domain.Opportunity) []domain.OpportunityAvailabilityReason {
	reasons := make([]domain.OpportunityAvailabilityReason, 0, 2)
	for _, venueName := range []string{opportunity.VenuePair.VenueA, opportunity.VenuePair.VenueB} {
		code := ""
		if c.fetchFailed[venueName] {
			code = domain.OpportunityReasonSourceFetchFailed
		} else if _, found := c.assetsByVenue[venueName][opportunity.Asset]; !found {
			code = domain.OpportunityReasonMarketDataUnavailable
		}
		if code != "" {
			reasons = append(reasons, domain.OpportunityAvailabilityReason{Code: code, Venue: venueName})
		}
	}
	return reasons
}

func canonicalVenuePair(a, b venue.MarketData) (venue.MarketData, venue.MarketData) {
	if a.Venue > b.Venue {
		return b, a
	}
	return a, b
}

func pairSourceRevisions(revisions map[string]uint64, venueA, venueB string) map[string]uint64 {
	return map[string]uint64{venueA: revisions[venueA], venueB: revisions[venueB]}
}

func nextUnavailableStatus(previous domain.OpportunityStatus) domain.OpportunityStatus {
	if previous == domain.OpportunityDegraded || previous == domain.OpportunityUnavailable {
		return domain.OpportunityUnavailable
	}
	return domain.OpportunityDegraded
}

func opportunityStatusCounts(opportunities []domain.Opportunity) (available, degraded, unavailable int) {
	for _, opportunity := range opportunities {
		switch opportunity.Status {
		case domain.OpportunityAvailable:
			available++
		case domain.OpportunityDegraded:
			degraded++
		case domain.OpportunityUnavailable:
			unavailable++
		}
	}
	return available, degraded, unavailable
}

func cloneOpportunity(opportunity domain.Opportunity) domain.Opportunity {
	opportunity.SourceRevisions = pairSourceRevisions(
		opportunity.SourceRevisions,
		opportunity.VenuePair.VenueA,
		opportunity.VenuePair.VenueB,
	)
	opportunity.AvailabilityReasons = append([]domain.OpportunityAvailabilityReason(nil), opportunity.AvailabilityReasons...)
	opportunity.RiskFlags = append([]string(nil), opportunity.RiskFlags...)
	opportunity.Warnings = append([]string(nil), opportunity.Warnings...)
	return opportunity
}

// isValid filters out snapshots that are stale or have broken data.
func isValid(md venue.MarketData, now time.Time) bool {
	if md.MarkPrice < minMarkPrice {
		return false
	}
	if md.IndexPrice < minMarkPrice {
		return false
	}
	if md.OpenInterest <= 0 {
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

	// Annualized gross edge from funding spread.
	annualizedGross := domain.AnnualizedGrossEdge(a.FundingRate, b.FundingRate)

	// Execution-aware sizing
	sizing := computeSizing(a, b, direction)
	longMarket, shortMarket := marketsForDirection(a, b, direction)
	slippageEstimate := estimateExecutionSlippage(longMarket, domain.SideLong, sizing.RecommendedNotional) +
		estimateExecutionSlippage(shortMarket, domain.SideShort, sizing.RecommendedNotional)
	feeEstimate := estimateFee(longMarket) + estimateFee(shortMarket)

	// Build warnings.
	var warnings []string
	if a.BidPrice == 0 || a.AskPrice == 0 {
		warnings = append(warnings, fmt.Sprintf("%s: missing bid/ask", a.Venue))
	}
	if b.BidPrice == 0 || b.AskPrice == 0 {
		warnings = append(warnings, fmt.Sprintf("%s: missing bid/ask", b.Venue))
	}

	confidence := classifyConfidence(a, b, now)
	riskTier := classifyRisk(annualizedGross, entrySpread)
	slippageLevel := domain.ClassifySlippage(entrySpread)
	liqCheck := CheckLiquidity(a, b, annualizedGross, now)

	// Separate blockers (hard) from risk flags (informational)
	blocked := false
	var riskFlags []string

	// Blockers — technically invalid, cannot execute
	hasBidAskMissing := (a.BidPrice == 0 || a.AskPrice == 0) || (b.BidPrice == 0 || b.AskPrice == 0)
	if hasBidAskMissing {
		warnings = append(warnings, "missing bid/ask data")
		blocked = true
	}
	if confidence == domain.ConfidenceLow {
		warnings = append(warnings, "low confidence: insufficient market data")
		blocked = true
	}
	if !domain.SlippageExecutable(slippageLevel) {
		warnings = append(warnings, fmt.Sprintf("entry cost %.2f%%: exceeds 5%% threshold", entrySpread*100))
		blocked = true
	}
	if liqCheck.Blocking {
		for _, r := range liqCheck.Reasons {
			warnings = append(warnings, r)
		}
		blocked = true
	}

	// Risk flags — non-blocking concerns, still executable
	switch slippageLevel {
	case domain.SlippageWarn:
		riskFlags = append(riskFlags, fmt.Sprintf("elevated slippage: %.2f%%", entrySpread*100))
	case domain.SlippageHigh:
		riskFlags = append(riskFlags, fmt.Sprintf("high slippage: %.2f%%", entrySpread*100))
	}
	if liqCheck.Suspect && !liqCheck.Blocking {
		riskFlags = append(riskFlags, liqCheck.Reasons...)
	}
	if riskTier == domain.RiskExperimental {
		riskFlags = append(riskFlags, "experimental risk tier")
	}
	if confidence == domain.ConfidenceMedium {
		riskFlags = append(riskFlags, "medium confidence: partial data freshness")
	}

	executionStatus := "executable"
	if blocked {
		executionStatus = "blocked"
	}

	id := fmt.Sprintf("%s-%s-%s", asset, a.Venue, b.Venue)

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
		SlippageEstimate:    slippageEstimate,
		FeeEstimate:         feeEstimate,
		AvailableNotional:   sizing.MaxAvailableNotional,
		BestPriceCapacity:   sizing.BestPriceCapacity,
		RecommendedNotional: sizing.RecommendedNotional,
		Liquidity:           sizing.Liquidity,
		Confidence:          confidence,
		RiskTier:            riskTier,
		MaxLeverage:         minLeverage(a.MaxLeverage, b.MaxLeverage),
		LiqSuspect:          liqCheck.Suspect,
		ExecutionStatus:     executionStatus,
		RiskFlags:           riskFlags,
		Warnings:            warnings,
	}
}

func minLeverage(a, b int) int {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a < b {
		return a
	}
	return b
}
