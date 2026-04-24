package paper

import (
	"context"
	"log/slog"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	monitorInterval = 10 * time.Second
	maxDuration     = 1 * time.Hour // paper mode max position duration
	minEdgeClose    = 0.01          // 1% annualized — close if edge drops below
)

type Monitor struct {
	executor *Executor
	store    PositionStore
	market   MarketDataSource
	logger   *slog.Logger
}

func NewMonitor(
	logger *slog.Logger,
	executor *Executor,
	store PositionStore,
	market MarketDataSource,
) *Monitor {
	return &Monitor{
		executor: executor,
		store:    store,
		market:   market,
		logger:   logger,
	}
}

// Run checks open positions periodically and closes them when conditions are met.
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

func (m *Monitor) check(ctx context.Context) {
	ids := m.store.OpenPositionIDs()
	for _, id := range ids {
		pos := m.store.Get(id)
		if pos == nil {
			continue
		}
		reason := m.shouldClose(ctx, pos)
		if reason == "" {
			continue
		}
		if err := m.executor.CloseByID(ctx, id, reason); err != nil {
			m.logger.Error("monitor: close failed", "id", id, "err", err)
		}
	}
}

func (m *Monitor) shouldClose(ctx context.Context, pos *Position) CloseReason {
	// 1. Degraded positions should close immediately
	if pos.State == StateDegraded {
		return CloseReasonDegraded
	}

	// 2. Max duration exceeded
	if pos.OpenedAt != nil && time.Since(*pos.OpenedAt) > maxDuration {
		return CloseReasonMaxDuration
	}

	// 3. Edge collapse
	if pos.Leg1Fill == nil || pos.Leg2Fill == nil {
		return ""
	}

	snapA, snapB, err := m.market.FreshSnapshots(ctx, pos.Asset, pos.VenuePair.VenueA, pos.VenuePair.VenueB)
	if err != nil {
		m.logger.Warn("monitor: failed to fetch snapshots", "id", pos.ID, "err", err)
		return ""
	}

	// Compute current annualized funding edge
	currentEdge := domain.AnnualizedGrossEdge(snapA.FundingRate, snapB.FundingRate)

	// Update P&L components on the stored position
	if snapA.BidPrice > 0 && snapB.BidPrice > 0 {
		stored := m.store.Get(pos.ID)
		if stored != nil && stored.Leg1Fill != nil && stored.Leg2Fill != nil {
			// Determine which snap maps to which leg
			var leg1Snap, leg2Snap venue.MarketData
			if stored.Leg1Fill.Venue == snapA.Venue {
				leg1Snap = snapA
				leg2Snap = snapB
			} else {
				leg1Snap = snapB
				leg2Snap = snapA
			}

			// Per-leg current prices and funding
			stored.Leg1Fill.CurrentFunding = leg1Snap.FundingRate
			stored.Leg2Fill.CurrentFunding = leg2Snap.FundingRate

			// Per-leg price P&L
			if stored.Leg1Fill.Side == domain.SideLong {
				stored.Leg1Fill.CurrentPrice = leg1Snap.BidPrice  // close long at bid
				stored.Leg2Fill.CurrentPrice = leg2Snap.AskPrice  // close short at ask
				stored.Leg1Fill.LegPricePnL = (leg1Snap.BidPrice - stored.Leg1Fill.FillPrice) / stored.Leg1Fill.FillPrice * stored.Leg1Fill.FilledSize
				stored.Leg2Fill.LegPricePnL = (stored.Leg2Fill.FillPrice - leg2Snap.AskPrice) / stored.Leg2Fill.FillPrice * stored.Leg2Fill.FilledSize
			} else {
				stored.Leg1Fill.CurrentPrice = leg1Snap.AskPrice  // close short at ask
				stored.Leg2Fill.CurrentPrice = leg2Snap.BidPrice  // close long at bid
				stored.Leg1Fill.LegPricePnL = (stored.Leg1Fill.FillPrice - leg1Snap.AskPrice) / stored.Leg1Fill.FillPrice * stored.Leg1Fill.FilledSize
				stored.Leg2Fill.LegPricePnL = (leg2Snap.BidPrice - stored.Leg2Fill.FillPrice) / stored.Leg2Fill.FillPrice * stored.Leg2Fill.FilledSize
			}
			stored.PricePnL = stored.Leg1Fill.LegPricePnL + stored.Leg2Fill.LegPricePnL

			// Per-leg accumulated funding
			if stored.OpenedAt != nil {
				hoursOpen := time.Since(*stored.OpenedAt).Hours()

				// Leg funding: long pays, short collects
				if stored.Leg1Fill.Side == domain.SideLong {
					stored.Leg1Fill.AccumFunding = -leg1Snap.FundingRate * hoursOpen * stored.Leg1Fill.FilledSize
					stored.Leg2Fill.AccumFunding = leg2Snap.FundingRate * hoursOpen * stored.Leg2Fill.FilledSize
				} else {
					stored.Leg1Fill.AccumFunding = leg1Snap.FundingRate * hoursOpen * stored.Leg1Fill.FilledSize
					stored.Leg2Fill.AccumFunding = -leg2Snap.FundingRate * hoursOpen * stored.Leg2Fill.FilledSize
				}
				stored.FundingPnL = stored.Leg1Fill.AccumFunding + stored.Leg2Fill.AccumFunding
			}

			// Next funding estimate (best-effort: next hour boundary)
			now := time.Now()
			nextHour := now.Truncate(time.Hour).Add(time.Hour)
			stored.Leg1Fill.NextFundingAt = &nextHour
			stored.Leg2Fill.NextFundingAt = &nextHour

			stored.TotalPnL = stored.PricePnL + stored.FundingPnL
			stored.CurrentSpread = currentEdge
			stored.HoldHours = ComputeHoldHours(stored)
			stored.EstBreakEvenHours = ComputeBreakEven(stored)
			if !stored.BreakEvenReached && stored.EstBreakEvenHours > 0 && stored.HoldHours >= stored.EstBreakEvenHours {
				stored.BreakEvenReached = true
			}
			stored.UpdatedAt = now
			m.store.Update(stored)
		}
	}

	if currentEdge < minEdgeClose {
		return CloseReasonEdgeCollapse
	}

	return ""
}
