package scanner

import (
	"context"
	"fmt"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const planTTL = 10 * time.Second

// BuildPlan creates an ExecutionPlan from a given opportunity ID using fresh market data.
func (s *Scanner) BuildPlan(ctx context.Context, opportunityID string) (*domain.ExecutionPlan, error) {
	// Find the opportunity
	opp := s.findOpportunity(opportunityID)
	if opp == nil {
		return nil, fmt.Errorf("opportunity not found: %s", opportunityID)
	}

	// Fetch fresh snapshots for both venues
	snapA, snapB, err := s.freshSnapshots(ctx, opp.Asset, opp.VenuePair.VenueA, opp.VenuePair.VenueB)
	if err != nil {
		return nil, fmt.Errorf("fetch fresh data: %w", err)
	}

	// Determine legs based on direction
	var longSnap, shortSnap venue.MarketData
	if opp.Direction == domain.DirectionLongA {
		longSnap = snapA
		shortSnap = snapB
	} else {
		longSnap = snapB
		shortSnap = snapA
	}

	// Build legs with fresh prices
	leg1 := domain.Leg{
		Venue:         longSnap.Venue,
		Asset:         longSnap.Asset,
		Side:          domain.SideLong,
		ExpectedPrice: longSnap.AskPrice, // buy at ask
		Slippage:      estimateSlippage(longSnap),
		Fee:           estimateFee(longSnap),
	}

	leg2 := domain.Leg{
		Venue:         shortSnap.Venue,
		Asset:         shortSnap.Asset,
		Side:          domain.SideShort,
		ExpectedPrice: shortSnap.BidPrice, // sell at bid
		Slippage:      estimateSlippage(shortSnap),
		Fee:           estimateFee(shortSnap),
	}

	// Compute spread from fresh prices
	var expectedSpread float64
	if leg1.ExpectedPrice > 0 {
		expectedSpread = (leg2.ExpectedPrice - leg1.ExpectedPrice) / leg1.ExpectedPrice
	}

	// Recompute net edge with fresh data
	fundingSpread := longSnap.FundingRate - shortSnap.FundingRate
	totalCosts := leg1.Slippage + leg1.Fee + leg2.Slippage + leg2.Fee
	estimatedNetEdge := (fundingSpread * hoursPerYear) - totalCosts

	// Confidence from fresh data
	now := time.Now()
	confidence := classifyConfidence(snapA, snapB, now)

	// Determine if still executable
	executable := confidence == domain.ConfidenceHigh

	var warnings []string
	if !hasBidAsk(snapA) {
		warnings = append(warnings, fmt.Sprintf("%s: missing bid/ask", snapA.Venue))
		executable = false
	}
	if !hasBidAsk(snapB) {
		warnings = append(warnings, fmt.Sprintf("%s: missing bid/ask", snapB.Venue))
		executable = false
	}

	// Use recommended notional from opportunity, or fall back
	notional := opp.RecommendedNotional
	if notional == 0 {
		notional = opp.AvailableNotional * 0.1 // conservative default: 10% of available
	}

	plan := &domain.ExecutionPlan{
		ID:               fmt.Sprintf("plan-%s-%d", opp.ID, now.UnixMilli()),
		OpportunityID:    opp.ID,
		Asset:            opp.Asset,
		Direction:        opp.Direction,
		Notional:         notional,
		Leg1:             leg1,
		Leg2:             leg2,
		ExpectedSpread:   expectedSpread,
		EstimatedNetEdge: estimatedNetEdge,
		Bounds: domain.Bounds{
			MaxSlippagePct:    0.005,  // 0.5%
			MaxEntrySpreadPct: 0.01,   // 1%
			MinNetEdgePct:     0.01,   // 1% annualized minimum
		},
		Confidence: confidence,
		Executable: executable,
		Warnings:   warnings,
		CreatedAt:  now,
		ExpiresAt:  now.Add(planTTL),
	}

	return plan, nil
}

func (s *Scanner) findOpportunity(id string) *domain.Opportunity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.opps {
		if s.opps[i].ID == id {
			opp := s.opps[i]
			return &opp
		}
	}
	return nil
}

func (s *Scanner) freshSnapshots(ctx context.Context, asset, venueA, venueB string) (venue.MarketData, venue.MarketData, error) {
	var snapA, snapB venue.MarketData
	var foundA, foundB bool

	for _, adapter := range s.adapters {
		name := adapter.Name()
		if name != venueA && name != venueB {
			continue
		}

		data, err := adapter.FetchMarketData(ctx)
		if err != nil {
			return snapA, snapB, fmt.Errorf("fetch %s: %w", name, err)
		}

		for _, md := range data {
			if md.Asset != asset {
				continue
			}
			if name == venueA {
				snapA = md
				foundA = true
			}
			if name == venueB {
				snapB = md
				foundB = true
			}
		}
	}

	if !foundA {
		return snapA, snapB, fmt.Errorf("no data for %s on %s", asset, venueA)
	}
	if !foundB {
		return snapA, snapB, fmt.Errorf("no data for %s on %s", asset, venueB)
	}

	return snapA, snapB, nil
}

func estimateSlippage(md venue.MarketData) float64 {
	if md.BidPrice <= 0 || md.AskPrice <= 0 {
		return 0
	}
	mid := (md.BidPrice + md.AskPrice) / 2
	return (md.AskPrice - md.BidPrice) / mid / 2 // half-spread as slippage estimate
}

func estimateFee(md venue.MarketData) float64 {
	// Placeholder: typical taker fee for perp venues
	_ = md
	return 0.0005 // 5bps
}
