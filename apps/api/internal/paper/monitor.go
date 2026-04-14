package paper

import (
	"context"
	"log/slog"
	"math"
	"time"
)

const (
	monitorInterval = 10 * time.Second
	maxDuration     = 1 * time.Hour     // paper mode max position duration
	minEdgeClose    = 0.01              // 1% annualized — close if edge drops below
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
	positions := m.store.OpenPositions()
	if len(positions) == 0 {
		return
	}

	for _, pos := range positions {
		reason := m.shouldClose(ctx, pos)
		if reason == "" {
			continue
		}
		go func(p *Position, r CloseReason) {
			if err := m.executor.Close(ctx, p, r); err != nil {
				m.logger.Error("monitor: close failed", "id", p.ID, "err", err)
			}
		}(pos, reason)
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

	// 3. Edge collapse — check if funding spread has dropped below threshold
	if pos.Leg1Fill == nil || pos.Leg2Fill == nil {
		return ""
	}

	snapA, snapB, err := m.market.FreshSnapshots(ctx, pos.Asset, pos.VenuePair.VenueA, pos.VenuePair.VenueB)
	if err != nil {
		m.logger.Warn("monitor: failed to fetch snapshots", "id", pos.ID, "err", err)
		return ""
	}

	// Compute current funding spread in the same direction as the position
	var currentEdge float64
	if pos.Leg1Fill.Side == "long" {
		// Leg1 is long (on venue A), leg2 is short (on venue B)
		// Edge = short funding - long funding
		currentEdge = math.Abs(snapB.FundingRate-snapA.FundingRate) * hoursPerYear
	} else {
		currentEdge = math.Abs(snapA.FundingRate-snapB.FundingRate) * hoursPerYear
	}

	// Update unrealized P&L
	if snapA.BidPrice > 0 && snapB.BidPrice > 0 {
		var currentLongPrice, currentShortPrice float64
		if pos.Leg1Fill.Side == "long" {
			currentLongPrice = snapA.BidPrice
			currentShortPrice = snapB.AskPrice
		} else {
			currentLongPrice = snapB.BidPrice
			currentShortPrice = snapA.AskPrice
		}
		longPnL := (currentLongPrice - pos.Leg1Fill.FillPrice) / pos.Leg1Fill.FillPrice * pos.Leg1Fill.FilledSize
		shortPnL := (pos.Leg2Fill.FillPrice - currentShortPrice) / pos.Leg2Fill.FillPrice * pos.Leg2Fill.FilledSize
		pos.UnrealizedPnL = longPnL + shortPnL
		pos.CurrentSpread = currentEdge
		pos.UpdatedAt = time.Now()
		m.store.Update(pos)
	}

	if currentEdge < minEdgeClose {
		return CloseReasonEdgeCollapse
	}

	return ""
}
