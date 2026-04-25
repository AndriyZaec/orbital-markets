package paper

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	minHedgeableFillPct = 0.50 // 50% minimum fill to continue
	maxHedgeMismatchPct = 0.05 // 5% acceptable mismatch
	maxWaitBetweenLegs  = 5 * time.Second
)

// MarketDataSource provides fresh market data for fill simulation.
type MarketDataSource interface {
	FreshSnapshots(ctx context.Context, asset, venueA, venueB string) (venue.MarketData, venue.MarketData, error)
}

// PositionStore is the interface for position persistence.
type PositionStore interface {
	Add(pos *Position)
	Get(id string) *Position
	Update(pos *Position)
	List() []*Position
	OpenPositionIDs() []string
}

type Executor struct {
	store  PositionStore
	market MarketDataSource
	logger *slog.Logger
}

func NewExecutor(
	logger *slog.Logger,
	store PositionStore,
	market MarketDataSource,
) *Executor {
	return &Executor{
		store:  store,
		market: market,
		logger: logger,
	}
}

// Execute runs the paper open flow synchronously. Call in a goroutine.
func (e *Executor) Execute(ctx context.Context, plan *domain.ExecutionPlan) (*Position, error) {
	pos := &Position{
		ID:             fmt.Sprintf("paper-%d", time.Now().UnixNano()),
		PlanID:         plan.ID,
		OpportunityID:  plan.OpportunityID,
		Asset:          plan.Asset,
		Direction:      plan.Direction,
		VenuePair:      domain.VenuePair{VenueA: plan.Leg1.Venue, VenueB: plan.Leg2.Venue},
		RiskTier:       plan.RiskTier,
		State:          StatePlanned,
		TargetNotional: plan.Notional,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	e.store.Add(pos)

	e.logger.Info("paper execution started", "id", pos.ID, "asset", pos.Asset)

	// Step 1: Submit leg 1 (riskier leg first — per EXECUTION.md)
	e.transition(pos, StateSubmittingLeg1, "starting leg 1")

	leg1Fill := e.simulateFill(plan.Leg1, plan.Notional)

	e.transition(pos, StateAwaitingLeg1Fill, "leg 1 submitted")

	simulateDelay(ctx)

	pos.Leg1Fill = leg1Fill
	e.store.Update(pos)

	// Check minimum hedgeable fill threshold
	if leg1Fill.FillRatio() < minHedgeableFillPct {
		e.transition(pos, StateFailed, fmt.Sprintf("leg 1 fill ratio %.1f%% below 50%% threshold", leg1Fill.FillRatio()*100))
		return pos, nil
	}

	e.logger.Info("leg 1 filled", "id", pos.ID, "ratio", fmt.Sprintf("%.1f%%", leg1Fill.FillRatio()*100))

	// Enforce maxWaitBetweenLegs — leg 2 must start within this window
	leg2Deadline := time.Now().Add(maxWaitBetweenLegs)
	leg2Ctx, leg2Cancel := context.WithDeadline(ctx, leg2Deadline)
	defer leg2Cancel()

	// Step 2: Submit leg 2 sized from actual leg 1 fill
	leg2TargetSize := leg1Fill.FilledSize // EXECUTION.md: leg 2 target = actual filled size of leg 1

	e.transition(pos, StateSubmittingLeg2, "starting leg 2")

	leg2Fill := e.simulateFillWithTimeout(leg2Ctx, plan.Leg2, leg2TargetSize)
	if leg2Fill == nil {
		// Timeout — could not submit leg 2 within window
		return e.handleLeg2Failure(ctx, pos, "leg 2 submission timed out (5s window)")
	}

	e.transition(pos, StateAwaitingLeg2Fill, "leg 2 submitted")

	simulateDelay(leg2Ctx)

	// Check if we've exceeded the inter-leg deadline
	if leg2Ctx.Err() != nil {
		return e.handleLeg2Failure(ctx, pos, "leg 2 confirmation exceeded 5s window")
	}

	pos.Leg2Fill = leg2Fill
	e.store.Update(pos)

	// Check hedge mismatch
	mismatch := math.Abs(leg2Fill.FilledSize-leg2TargetSize) / leg2TargetSize
	pos.HedgeMismatch = mismatch
	e.store.Update(pos)

	if mismatch > maxHedgeMismatchPct {
		// Retry once per EXECUTION.md
		e.transition(pos, StateRetryingLeg2, fmt.Sprintf("hedge mismatch %.1f%% exceeds 5%%", mismatch*100))

		retryFill := e.simulateFill(plan.Leg2, leg2TargetSize)
		pos.Leg2Fill = retryFill
		mismatch = math.Abs(retryFill.FilledSize-leg2TargetSize) / leg2TargetSize
		pos.HedgeMismatch = mismatch
		e.store.Update(pos)

		if mismatch > maxHedgeMismatchPct {
			return e.handleLeg2Failure(ctx, pos, fmt.Sprintf("hedge mismatch %.1f%% after retry", mismatch*100))
		}
	}

	// Success — compute entry spread and entry basis
	if pos.Leg1Fill.FillPrice > 0 && pos.Leg2Fill.FillPrice > 0 {
		pos.EntrySpread = (pos.Leg2Fill.FillPrice - pos.Leg1Fill.FillPrice) / pos.Leg1Fill.FillPrice
		// Basis = (leg1 price - leg2 price) / leg1 price
		// Measures the relative price gap between venues at entry
		mid := (pos.Leg1Fill.FillPrice + pos.Leg2Fill.FillPrice) / 2
		pos.EntryBasis = (pos.Leg1Fill.FillPrice - pos.Leg2Fill.FillPrice) / mid
		pos.CurrentBasis = pos.EntryBasis
	}

	now := time.Now()
	pos.OpenedAt = &now
	e.transition(pos, StateOpen, "hedge integrity confirmed")

	e.logger.Info("paper position opened", "id", pos.ID, "mismatch", fmt.Sprintf("%.2f%%", mismatch*100))
	return pos, nil
}

// CloseByID closes a position by ID. Safe for concurrent use.
func (e *Executor) CloseByID(ctx context.Context, id string, reason CloseReason) error {
	pos := e.store.Get(id)
	if pos == nil {
		return fmt.Errorf("position not found: %s", id)
	}
	if pos.State != StateOpen && pos.State != StateDegraded {
		return fmt.Errorf("cannot close position in state %s", pos.State)
	}

	pos.CloseReason = reason
	e.transition(pos, StatePendingClose, fmt.Sprintf("close requested: %s", reason))
	e.transition(pos, StateClosing, "closing legs")

	// Compute realized P&L from entry vs current prices
	snapA, snapB, err := e.market.FreshSnapshots(ctx, pos.Asset, pos.VenuePair.VenueA, pos.VenuePair.VenueB)
	if err != nil {
		e.logger.Error("close: failed to fetch market data", "id", pos.ID, "err", err)
	}

	if pos.Leg1Fill != nil && pos.Leg2Fill != nil && err == nil {
		var currentLongPrice, currentShortPrice float64
		if pos.Leg1Fill.Side == domain.SideLong {
			currentLongPrice = snapA.BidPrice
			currentShortPrice = snapB.AskPrice
		} else {
			currentLongPrice = snapB.BidPrice
			currentShortPrice = snapA.AskPrice
		}

		// Price P&L
		longPnL := (currentLongPrice - pos.Leg1Fill.FillPrice) / pos.Leg1Fill.FillPrice * pos.Leg1Fill.FilledSize
		shortPnL := (pos.Leg2Fill.FillPrice - currentShortPrice) / pos.Leg2Fill.FillPrice * pos.Leg2Fill.FilledSize
		pos.PricePnL = longPnL + shortPnL

		// Funding P&L at close = use last computed value (accrued by monitor)
		pos.RealizedPnL = pos.PricePnL + pos.FundingPnL
		pos.TotalPnL = pos.RealizedPnL
	}

	simulateDelay(ctx)

	now := time.Now()
	pos.ClosedAt = &now
	pos.HoldHours = ComputeHoldHours(pos)
	e.transition(pos, StateClosed, fmt.Sprintf("closed: %s", reason))

	e.logger.Info("paper position closed", "id", pos.ID, "reason", reason, "pnl", pos.RealizedPnL)
	return nil
}

func (e *Executor) handleLeg2Failure(ctx context.Context, pos *Position, reason string) (*Position, error) {
	e.transition(pos, StateUnwinding, reason)

	e.logger.Warn("paper: unwinding leg 1", "id", pos.ID, "reason", reason)

	simulateDelay(ctx)

	e.transition(pos, StateDegraded, "unwind attempted, hedge incomplete")

	return pos, nil
}

// transition updates state and persists atomically through the store.
func (e *Executor) transition(pos *Position, to ExecState, reason string) {
	pos.transition(to, reason)
	e.store.Update(pos)
}

// simulateFill produces a fill with realistic failure distribution.
//
// Distribution:
//   - 5% chance: sub-50% fill (exercises minimum hedgeable threshold)
//   - 10% chance: 50-90% fill (partial fill, may cause hedge mismatch)
//   - 85% chance: 90-100% fill (normal execution)
func (e *Executor) simulateFill(leg domain.Leg, targetSize float64) *Fill {
	slippagePct := rand.Float64() * 0.003 // 0-0.3%

	// Fill ratio with realistic failure distribution
	roll := rand.Float64()
	var fillRatio float64
	switch {
	case roll < 0.05: // 5% chance: catastrophic partial fill
		fillRatio = 0.1 + rand.Float64()*0.35 // 10-45%
	case roll < 0.15: // 10% chance: significant partial fill
		fillRatio = 0.50 + rand.Float64()*0.40 // 50-90%
	default: // 85% chance: normal fill
		fillRatio = 0.90 + rand.Float64()*0.10 // 90-100%
	}

	var fillPrice float64
	if leg.Side == domain.SideLong {
		fillPrice = leg.ExpectedPrice * (1 + slippagePct)
	} else {
		fillPrice = leg.ExpectedPrice * (1 - slippagePct)
	}

	return &Fill{
		Venue:      leg.Venue,
		Side:       leg.Side,
		TargetSize: targetSize,
		FilledSize: targetSize * fillRatio,
		FillPrice:  fillPrice,
		Slippage:   slippagePct,
		Fee:        leg.Fee,
		FilledAt:   time.Now(),
	}
}

// simulateFillWithTimeout returns nil if context expires before fill completes.
func (e *Executor) simulateFillWithTimeout(ctx context.Context, leg domain.Leg, targetSize float64) *Fill {
	// 5% chance of timeout (simulates venue being unreachable within window)
	if rand.Float64() < 0.05 {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(maxWaitBetweenLegs + time.Second): // intentionally exceed deadline
			return nil
		}
	}

	return e.simulateFill(leg, targetSize)
}

func simulateDelay(ctx context.Context) {
	delay := 100 + rand.IntN(400) // 100-500ms
	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(delay) * time.Millisecond):
	}
}
