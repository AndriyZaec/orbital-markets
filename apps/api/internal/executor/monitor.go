package executor

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	liveMonitorInterval = 10 * time.Second
)

// MarketSource provides fresh market data for monitoring.
type MarketSource interface {
	FreshSnapshots(ctx context.Context, asset, venueA, venueB string) (venue.MarketData, venue.MarketData, error)
}

type LiquidationSource interface {
	LiquidationPrices(ctx context.Context, position *LivePosition) (map[string]float64, error)
}

// Monitor periodically evaluates open live positions against real venue data
// and persists updated metrics to SQLite.
type Monitor struct {
	store        *Store
	market       MarketSource
	liquidations LiquidationSource
	logger       *slog.Logger
}

func NewMonitor(logger *slog.Logger, store *Store, market MarketSource, liquidations LiquidationSource) *Monitor {
	return &Monitor{
		store: store, market: market, liquidations: liquidations, logger: logger,
	}
}

// Run checks open live positions every tick until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(liveMonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *Monitor) tick(ctx context.Context) {
	positions, err := m.store.ListOpenPositions(ctx)
	if err != nil {
		m.logger.Error("live monitor: list open positions", "err", err)
		return
	}
	for i := range positions {
		m.evaluate(ctx, &positions[i])
	}
}

func (m *Monitor) evaluate(ctx context.Context, pos *LivePosition) {
	// Need fills to compute anything meaningful
	fills, err := m.store.GetFills(ctx, pos.ID)
	if err != nil {
		m.logger.Warn("live monitor: get fills", "err", err, "id", pos.ID)
		return
	}

	var leg1, leg2 *LiveFill
	for i := range fills {
		if fills[i].Leg == 1 {
			leg1 = &fills[i]
		} else if fills[i].Leg == 2 {
			leg2 = &fills[i]
		}
	}

	if leg1 == nil || leg2 == nil || !leg1.Filled || !leg2.Filled {
		return
	}

	// Fetch fresh venue data
	snapA, snapB, err := m.market.FreshSnapshots(ctx, pos.Asset, pos.VenueA, pos.VenueB)
	if err != nil {
		m.logger.Warn("live monitor: fetch snapshots", "err", err, "id", pos.ID)
		return
	}

	// Match snaps to legs by venue
	var leg1Snap, leg2Snap venue.MarketData
	if leg1.Venue == snapA.Venue {
		leg1Snap = snapA
		leg2Snap = snapB
	} else {
		leg1Snap = snapB
		leg2Snap = snapA
	}

	if leg1Snap.MarkPrice <= 0 || leg2Snap.MarkPrice <= 0 {
		return
	}

	update := MonitorUpdate{}
	leg1Side := domain.Side(leg1.Side)
	leg2Side := domain.Side(leg2.Side)

	// Preserve the open position's direction so a funding reversal becomes negative carry.
	update.CurrentSpread = currentFundingAPR(leg1Side, leg1Snap.FundingRate, leg2Side, leg2Snap.FundingRate)

	// Venue UIs report unrealized PnL against mark price, not executable BBO.
	update.Leg1CurPrice = leg1Snap.MarkPrice
	update.Leg2CurPrice = leg2Snap.MarkPrice

	update.PricePnL = legPricePnL(leg1Side, leg1.AvgFillPrice, update.Leg1CurPrice, leg1.FilledAmount) +
		legPricePnL(leg2Side, leg2.AvgFillPrice, update.Leg2CurPrice, leg2.FilledAmount)

	// Basis: relative price gap between venues
	if update.Leg1CurPrice > 0 && update.Leg2CurPrice > 0 {
		mid := (update.Leg1CurPrice + update.Leg2CurPrice) / 2
		update.CurrentBasis = (update.Leg1CurPrice - update.Leg2CurPrice) / mid

		// Entry basis from fill prices
		entryMid := (leg1.AvgFillPrice + leg2.AvgFillPrice) / 2
		if entryMid > 0 {
			update.EntryBasis = (leg1.AvgFillPrice - leg2.AvgFillPrice) / entryMid
		}
		update.BasisChange = update.CurrentBasis - update.EntryBasis
	}

	var nativeLiquidationPrices map[string]float64
	if m.liquidations != nil {
		nativeLiquidationPrices, err = m.liquidations.LiquidationPrices(ctx, pos)
		if err != nil {
			m.logger.Warn("live monitor: fetch liquidation prices", "err", err, "id", pos.ID)
		}
	}
	update.Leg1LiqPrice = monitoredLiquidationPrice(
		nativeLiquidationPrices[leg1.Venue], leg1.AvgFillPrice, leg1Side, pos.Leverage,
	)
	update.Leg2LiqPrice = monitoredLiquidationPrice(
		nativeLiquidationPrices[leg2.Venue], leg2.AvgFillPrice, leg2Side, pos.Leverage,
	)

	update.Leg1LiqDist = domain.LiquidationDistance(update.Leg1CurPrice, update.Leg1LiqPrice, leg1Side)
	update.Leg2LiqDist = domain.LiquidationDistance(update.Leg2CurPrice, update.Leg2LiqPrice, leg2Side)

	update.Leg1LiqRisk = string(domain.ClassifyLiqRisk(update.Leg1LiqDist, update.Leg1LiqPrice))
	update.Leg2LiqRisk = string(domain.ClassifyLiqRisk(update.Leg2LiqDist, update.Leg2LiqPrice))

	// Hold hours
	if pos.OpenedAt != "" {
		openedAt, _ := time.Parse(time.RFC3339, pos.OpenedAt)
		if !openedAt.IsZero() {
			update.HoldHours = time.Since(openedAt).Hours()
		}
	}

	// Persist
	m.store.UpdateMonitoring(ctx, pos.ID, update)

	// Log significant changes
	if update.Leg1LiqRisk == string(domain.LiqRiskWarning) || update.Leg1LiqRisk == string(domain.LiqRiskCritical) ||
		update.Leg2LiqRisk == string(domain.LiqRiskWarning) || update.Leg2LiqRisk == string(domain.LiqRiskCritical) {
		m.logger.Warn("live monitor: liquidation risk",
			"id", pos.ID,
			"asset", pos.Asset,
			"leg1_risk", update.Leg1LiqRisk,
			"leg1_dist", fmt.Sprintf("%.1f%%", update.Leg1LiqDist*100),
			"leg2_risk", update.Leg2LiqRisk,
			"leg2_dist", fmt.Sprintf("%.1f%%", update.Leg2LiqDist*100),
		)
	}

	if math.Abs(update.BasisChange) > 0.01 {
		m.logger.Warn("live monitor: significant basis drift",
			"id", pos.ID,
			"asset", pos.Asset,
			"basis_change", fmt.Sprintf("%.2f%%", update.BasisChange*100),
		)
	}
}

func currentFundingAPR(leg1Side domain.Side, leg1Rate float64, leg2Side domain.Side, leg2Rate float64) float64 {
	switch {
	case leg1Side == domain.SideShort && leg2Side == domain.SideLong:
		return domain.AnnualizeRate(domain.CarryEdgePerHour(leg1Rate, leg2Rate))
	case leg1Side == domain.SideLong && leg2Side == domain.SideShort:
		return domain.AnnualizeRate(domain.CarryEdgePerHour(leg2Rate, leg1Rate))
	default:
		return 0
	}
}

func legPricePnL(side domain.Side, entryPrice, markPrice, baseAmount float64) float64 {
	if side == domain.SideShort {
		return (entryPrice - markPrice) * baseAmount
	}
	return (markPrice - entryPrice) * baseAmount
}

func monitoredLiquidationPrice(nativePrice, entryPrice float64, side domain.Side, leverage float64) float64 {
	if nativePrice > 0 {
		return nativePrice
	}
	return domain.LiquidationPrice(entryPrice, side, leverage)
}
