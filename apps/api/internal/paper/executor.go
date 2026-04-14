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

type Executor struct {
	store  *Store
	market MarketDataSource
	logger *slog.Logger
}

func NewExecutor(logger *slog.Logger, store *Store, market MarketDataSource) *Executor {
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
		State:          StatePlanned,
		TargetNotional: plan.Notional,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	e.store.Add(pos)

	e.logger.Info("paper execution started", "id", pos.ID, "asset", pos.Asset)

	// Step 1: Submit leg 1 (riskier leg first — per EXECUTION.md)
	pos.transition(StateSubmittingLeg1, "starting leg 1")
	e.store.Update(pos)

	leg1Fill, err := e.simulateFill(ctx, plan.Leg1, plan.Notional, plan.Leg1.Venue, plan.Asset)
	if err != nil {
		pos.transition(StateFailed, fmt.Sprintf("leg 1 fill error: %s", err))
		e.store.Update(pos)
		return pos, nil
	}

	pos.transition(StateAwaitingLeg1Fill, "leg 1 submitted")
	e.store.Update(pos)

	// Simulate fill delay
	simulateDelay(ctx)

	pos.Leg1Fill = leg1Fill

	// Check minimum hedgeable fill threshold
	if leg1Fill.FillRatio() < minHedgeableFillPct {
		pos.transition(StateFailed, fmt.Sprintf("leg 1 fill ratio %.1f%% below 50%% threshold", leg1Fill.FillRatio()*100))
		e.store.Update(pos)
		return pos, nil
	}

	e.logger.Info("leg 1 filled", "id", pos.ID, "ratio", fmt.Sprintf("%.1f%%", leg1Fill.FillRatio()*100))

	// Step 2: Submit leg 2 sized from actual leg 1 fill
	leg2TargetSize := leg1Fill.FilledSize // EXECUTION.md: leg 2 target = actual filled size of leg 1

	pos.transition(StateSubmittingLeg2, "starting leg 2")
	e.store.Update(pos)

	leg2Fill, err := e.simulateFill(ctx, plan.Leg2, leg2TargetSize, plan.Leg2.Venue, plan.Asset)
	if err != nil {
		pos.transition(StateRetryingLeg2, fmt.Sprintf("leg 2 error: %s, retrying", err))
		e.store.Update(pos)

		// Retry once per EXECUTION.md
		leg2Fill, err = e.simulateFill(ctx, plan.Leg2, leg2TargetSize, plan.Leg2.Venue, plan.Asset)
		if err != nil {
			return e.handleLeg2Failure(ctx, pos, plan, "retry also failed")
		}
	}

	pos.transition(StateAwaitingLeg2Fill, "leg 2 submitted")
	e.store.Update(pos)

	simulateDelay(ctx)

	pos.Leg2Fill = leg2Fill

	// Check hedge mismatch
	mismatch := math.Abs(leg2Fill.FilledSize-leg2TargetSize) / leg2TargetSize
	pos.HedgeMismatch = mismatch

	if mismatch > maxHedgeMismatchPct {
		// Retry once
		pos.transition(StateRetryingLeg2, fmt.Sprintf("hedge mismatch %.1f%% exceeds 5%%", mismatch*100))
		e.store.Update(pos)

		leg2Fill, _ = e.simulateFill(ctx, plan.Leg2, leg2TargetSize, plan.Leg2.Venue, plan.Asset)
		pos.Leg2Fill = leg2Fill
		mismatch = math.Abs(leg2Fill.FilledSize-leg2TargetSize) / leg2TargetSize
		pos.HedgeMismatch = mismatch

		if mismatch > maxHedgeMismatchPct {
			return e.handleLeg2Failure(ctx, pos, plan, fmt.Sprintf("hedge mismatch %.1f%% after retry", mismatch*100))
		}
	}

	// Success — compute entry spread
	if pos.Leg1Fill.FillPrice > 0 {
		pos.EntrySpread = (pos.Leg2Fill.FillPrice - pos.Leg1Fill.FillPrice) / pos.Leg1Fill.FillPrice
	}

	now := time.Now()
	pos.OpenedAt = &now
	pos.transition(StateOpen, "hedge integrity confirmed")
	e.store.Update(pos)

	e.logger.Info("paper position opened", "id", pos.ID, "mismatch", fmt.Sprintf("%.2f%%", mismatch*100))
	return pos, nil
}

// Close executes the close flow for an open or degraded position.
func (e *Executor) Close(ctx context.Context, pos *Position, reason CloseReason) error {
	if pos.State != StateOpen && pos.State != StateDegraded {
		return fmt.Errorf("cannot close position in state %s", pos.State)
	}

	pos.CloseReason = reason
	pos.transition(StatePendingClose, fmt.Sprintf("close requested: %s", reason))
	e.store.Update(pos)

	pos.transition(StateClosing, "closing legs")
	e.store.Update(pos)

	// Simulate close fills using current market prices
	snapA, snapB, err := e.market.FreshSnapshots(ctx, pos.Asset, pos.VenuePair.VenueA, pos.VenuePair.VenueB)
	if err != nil {
		e.logger.Error("close: failed to fetch market data", "id", pos.ID, "err", err)
	}

	// Compute realized P&L from entry vs current prices
	if pos.Leg1Fill != nil && pos.Leg2Fill != nil && err == nil {
		var currentLongPrice, currentShortPrice float64
		if pos.Leg1Fill.Side == domain.SideLong {
			currentLongPrice = snapA.BidPrice  // close long at bid
			currentShortPrice = snapB.AskPrice // close short at ask
		} else {
			currentLongPrice = snapB.BidPrice
			currentShortPrice = snapA.AskPrice
		}

		longPnL := (currentLongPrice - pos.Leg1Fill.FillPrice) / pos.Leg1Fill.FillPrice * pos.Leg1Fill.FilledSize
		shortPnL := (pos.Leg2Fill.FillPrice - currentShortPrice) / pos.Leg2Fill.FillPrice * pos.Leg2Fill.FilledSize
		pos.RealizedPnL = longPnL + shortPnL
	}

	simulateDelay(ctx)

	now := time.Now()
	pos.ClosedAt = &now
	pos.transition(StateClosed, fmt.Sprintf("closed: %s", reason))
	e.store.Update(pos)

	e.logger.Info("paper position closed", "id", pos.ID, "reason", reason, "pnl", pos.RealizedPnL)
	return nil
}

func (e *Executor) handleLeg2Failure(ctx context.Context, pos *Position, plan *domain.ExecutionPlan, reason string) (*Position, error) {
	pos.transition(StateUnwinding, reason)
	e.store.Update(pos)

	e.logger.Warn("paper: unwinding leg 1", "id", pos.ID, "reason", reason)

	// Simulate unwind of leg 1
	simulateDelay(ctx)

	// Mark as degraded — unwind is best-effort in paper mode
	pos.transition(StateDegraded, "unwind attempted, hedge incomplete")
	e.store.Update(pos)

	return pos, nil
}

func (e *Executor) simulateFill(ctx context.Context, leg domain.Leg, targetSize float64, venueName, asset string) (*Fill, error) {
	// Simulate fill with bounded noise
	slippagePct := rand.Float64() * 0.003 // 0-0.3% slippage
	fillRatio := 0.85 + rand.Float64()*0.15 // 85-100% fill

	var fillPrice float64
	if leg.Side == domain.SideLong {
		fillPrice = leg.ExpectedPrice * (1 + slippagePct) // buy slightly higher
	} else {
		fillPrice = leg.ExpectedPrice * (1 - slippagePct) // sell slightly lower
	}

	return &Fill{
		Venue:      venueName,
		Side:       leg.Side,
		TargetSize: targetSize,
		FilledSize: targetSize * fillRatio,
		FillPrice:  fillPrice,
		Slippage:   slippagePct,
		Fee:        leg.Fee,
		FilledAt:   time.Now(),
	}, nil
}

func simulateDelay(ctx context.Context) {
	delay := 100 + rand.IntN(400) // 100-500ms
	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(delay) * time.Millisecond):
	}
}
