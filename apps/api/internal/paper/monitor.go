package paper

import (
	"context"
	"log/slog"
	"math"
	"time"
)

const (
	monitorInterval = 10 * time.Second
	maxDuration     = 1 * time.Hour // paper mode max position duration
	minEdgeClose    = 0.01          // 1% annualized — close if edge drops below
	hoursPerYear    = 8760.0
)

type Monitor struct {
	executor *Executor
	store    *Store
	market   MarketDataSource
	logger   *slog.Logger
}

func NewMonitor(logger *slog.Logger, executor *Executor, store *Store, market MarketDataSource) *Monitor {
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

	// Compute current funding edge
	var currentEdge float64
	if pos.Leg1Fill.Side == "long" {
		currentEdge = math.Abs(snapB.FundingRate-snapA.FundingRate) * hoursPerYear
	} else {
		currentEdge = math.Abs(snapA.FundingRate-snapB.FundingRate) * hoursPerYear
	}

	// Update unrealized P&L on the stored position
	if snapA.BidPrice > 0 && snapB.BidPrice > 0 {
		stored := m.store.Get(pos.ID)
		if stored != nil {
			var currentLongPrice, currentShortPrice float64
			if stored.Leg1Fill.Side == "long" {
				currentLongPrice = snapA.BidPrice
				currentShortPrice = snapB.AskPrice
			} else {
				currentLongPrice = snapB.BidPrice
				currentShortPrice = snapA.AskPrice
			}
			longPnL := (currentLongPrice - stored.Leg1Fill.FillPrice) / stored.Leg1Fill.FillPrice * stored.Leg1Fill.FilledSize
			shortPnL := (stored.Leg2Fill.FillPrice - currentShortPrice) / stored.Leg2Fill.FillPrice * stored.Leg2Fill.FilledSize
			stored.UnrealizedPnL = longPnL + shortPnL
			stored.CurrentSpread = currentEdge
			stored.UpdatedAt = time.Now()
			m.store.Update(stored)
		}
	}

	if currentEdge < minEdgeClose {
		return CloseReasonEdgeCollapse
	}

	return ""
}
