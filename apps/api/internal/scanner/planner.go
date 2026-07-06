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
// leverageLong / leverageShort are each clamped to allowed range (1x-5x); pass 0 to
// default to 1x on that leg. requestedNotional is the user-entered notional per leg;
// pass 0 to fall back to the opportunity's recommended notional. If > 0 it is used
// verbatim for BOTH legs — position size stays equal across legs; only leverage /
// margin / liquidation vary between them.
func (s *Scanner) BuildPlan(
	ctx context.Context,
	opportunityID string,
	leverageLong float64,
	leverageShort float64,
	requestedNotional float64,
) (*domain.ExecutionPlan, error) {
	// Find the opportunity
	opp := s.FindOpportunity(opportunityID)
	if opp == nil {
		return nil, fmt.Errorf("opportunity not found: %s", opportunityID)
	}

	// Fetch fresh snapshots for both venues
	snapA, snapB, err := s.FreshSnapshots(ctx, opp.Asset, opp.VenuePair.VenueA, opp.VenuePair.VenueB)
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
	grossEdge := domain.AnnualizedGrossEdge(shortSnap.FundingRate, longSnap.FundingRate)
	totalCosts := leg1.Slippage + leg1.Fee + leg2.Slippage + leg2.Fee
	estimatedNetEdge := domain.EstimatedNetEdge(grossEdge, totalCosts)

	// Confidence from fresh data
	now := time.Now()
	confidence := classifyConfidence(snapA, snapB, now)

	// Slippage classification on fresh data
	slippageLevel := domain.ClassifySlippage(totalCosts)

	var warnings []string
	if !hasBidAsk(snapA) {
		warnings = append(warnings, fmt.Sprintf("%s: missing bid/ask", snapA.Venue))
	}
	if !hasBidAsk(snapB) {
		warnings = append(warnings, fmt.Sprintf("%s: missing bid/ask", snapB.Venue))
	}
	switch slippageLevel {
	case domain.SlippageWarn:
		warnings = append(warnings, fmt.Sprintf("entry cost %.2f%%: elevated slippage", totalCosts*100))
	case domain.SlippageHigh:
		warnings = append(warnings, fmt.Sprintf("entry cost %.2f%%: high slippage", totalCosts*100))
	case domain.SlippageBlock:
		warnings = append(warnings, fmt.Sprintf("entry cost %.2f%%: exceeds 5%% threshold", totalCosts*100))
	}

	hasMissingBidAsk := !hasBidAsk(snapA) || !hasBidAsk(snapB)
	executable := confidence == domain.ConfidenceHigh &&
		!hasMissingBidAsk &&
		domain.SlippageExecutable(slippageLevel)

	notional := opp.RecommendedNotional
	if requestedNotional > 0 {
		notional = requestedNotional
	}

	if leverageLong <= 0 {
		leverageLong = domain.DefaultLeverage
	}
	if leverageShort <= 0 {
		leverageShort = domain.DefaultLeverage
	}
	// Per-leg leverage configs (leverage is clamped to [Min,Max] inside).
	longCfg := domain.ComputeLeverage(notional, leverageLong)
	shortCfg := domain.ComputeLeverage(notional, leverageShort)

	// leg1 is long, leg2 is short (see leg construction above).
	leg1.Leverage = longCfg.Leverage
	leg1.MarginRequired = notional / longCfg.Leverage
	leg2.Leverage = shortCfg.Leverage
	leg2.MarginRequired = notional / shortCfg.Leverage

	// Aggregate config for backward-compat top-level Leverage field.
	// MarginRequired = sum of leg margins; GrossExposure stays 2×notional.
	totalMargin := leg1.MarginRequired + leg2.MarginRequired
	exposure := notional * 2
	effective := 0.0
	if totalMargin > 0 {
		effective = exposure / totalMargin
	}
	// Top-level Leverage.Leverage: keep as the long leg for legacy display
	// (frontend now reads per-leg values; this is a compatibility surface).
	levConfig := domain.LeverageConfig{
		Leverage:          leg1.Leverage,
		MarginRequired:    totalMargin,
		GrossExposure:     exposure,
		EffectiveLeverage: effective,
	}

	// Attach estimated liquidation to each leg at THIS leg's leverage.
	// We use ExpectedPrice as the reference "current" price — plan is built
	// from a fresh snapshot, so at plan time entry ≈ current.
	leg1.LiquidationPrice = domain.LiquidationPrice(leg1.ExpectedPrice, leg1.Side, leg1.Leverage)
	leg1.LiquidationDistance = domain.LiquidationDistance(leg1.ExpectedPrice, leg1.LiquidationPrice, leg1.Side)
	leg1.LiquidationRisk = domain.ClassifyLiqRisk(leg1.LiquidationDistance, leg1.LiquidationPrice)
	leg2.LiquidationPrice = domain.LiquidationPrice(leg2.ExpectedPrice, leg2.Side, leg2.Leverage)
	leg2.LiquidationDistance = domain.LiquidationDistance(leg2.ExpectedPrice, leg2.LiquidationPrice, leg2.Side)
	leg2.LiquidationRisk = domain.ClassifyLiqRisk(leg2.LiquidationDistance, leg2.LiquidationPrice)

	plan := &domain.ExecutionPlan{
		ID:            fmt.Sprintf("plan-%s-%d", opp.ID, now.UnixMilli()),
		OpportunityID: opp.ID,
		Asset:         opp.Asset,
		Direction:     opp.Direction,
		Notional:      notional,
		Leverage:      levConfig,
		Leg1:          leg1,
		Leg2:          leg2,
		ExpectedSpread:   expectedSpread,
		EstimatedNetEdge: estimatedNetEdge,
		Bounds: domain.Bounds{
			MaxSlippagePct:    0.005,  // 0.5%
			MaxEntrySpreadPct: 0.01,   // 1%
			MinNetEdgePct:     0.01,   // 1% annualized minimum
		},
		RiskTier:   opp.RiskTier,
		Confidence: confidence,
		Executable: executable,
		Warnings:   warnings,
		CreatedAt:  now,
		ExpiresAt:  now.Add(planTTL),
	}

	return plan, nil
}

// FindOpportunity returns a copy of the opportunity with the given ID, or nil.
func (s *Scanner) FindOpportunity(id string) *domain.Opportunity {
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

// FreshSnapshots fetches current market data for a given asset from two venues.
func (s *Scanner) FreshSnapshots(ctx context.Context, asset, venueA, venueB string) (venue.MarketData, venue.MarketData, error) {
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
