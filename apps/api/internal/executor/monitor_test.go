package executor

import (
	"context"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"testing"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type monitorMarketSource struct{}

func (monitorMarketSource) FreshSnapshots(context.Context, string, string, string) (venue.MarketData, venue.MarketData, error) {
	return venue.MarketData{Venue: "pacifica", MarkPrice: 100},
		venue.MarketData{Venue: "hyperliquid", MarkPrice: 100}, nil
}

type monitorLiquidationSource map[string]float64

func (s monitorLiquidationSource) LiquidationPrices(context.Context, *LivePosition) (map[string]float64, error) {
	return s, nil
}

func TestLegPricePnLReturnsQuoteCurrencyValue(t *testing.T) {
	tests := []struct {
		name     string
		side     domain.Side
		entry    float64
		mark     float64
		amount   float64
		expected float64
	}{
		{name: "long loss", side: domain.SideLong, entry: 100, mark: 99.30, amount: 2, expected: -1.40},
		{name: "short gain", side: domain.SideShort, entry: 100, mark: 99.325, amount: 2, expected: 1.35},
	}

	var total float64
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := legPricePnL(test.side, test.entry, test.mark, test.amount)
			if math.Abs(got-test.expected) > 1e-9 {
				t.Fatalf("price PnL = %v, want %v", got, test.expected)
			}
			total += got
		})
	}
	if math.Abs(total-(-0.05)) > 1e-9 {
		t.Fatalf("net price PnL = %v, want -0.05", total)
	}
}

func TestCurrentFundingAPRPreservesPositionDirection(t *testing.T) {
	tests := []struct {
		name     string
		leg1Side domain.Side
		leg1Rate float64
		leg2Side domain.Side
		leg2Rate float64
		expected float64
	}{
		{name: "short leg 1 receives positive carry", leg1Side: domain.SideShort, leg1Rate: 0.000010, leg2Side: domain.SideLong, leg2Rate: 0.000002, expected: 0.07008},
		{name: "short leg 1 pays after funding flip", leg1Side: domain.SideShort, leg1Rate: 0.000003, leg2Side: domain.SideLong, leg2Rate: 0.000006, expected: -0.02628},
		{name: "short leg 2 pays after funding flip", leg1Side: domain.SideLong, leg1Rate: 0.000006, leg2Side: domain.SideShort, leg2Rate: 0.000003, expected: -0.02628},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := currentFundingAPR(test.leg1Side, test.leg1Rate, test.leg2Side, test.leg2Rate)
			if math.Abs(got-test.expected) > 1e-9 {
				t.Fatalf("current funding APR = %v, want %v", got, test.expected)
			}
		})
	}
}

func TestMonitoredLiquidationPricePrefersNativeVenueValue(t *testing.T) {
	if got := monitoredLiquidationPrice(72, 100, domain.SideLong, 5); got != 72 {
		t.Fatalf("native liquidation price = %v, want 72", got)
	}
	fallback := domain.LiquidationPrice(100, domain.SideLong, 5)
	if got := monitoredLiquidationPrice(0, 100, domain.SideLong, 5); got != fallback {
		t.Fatalf("fallback liquidation price = %v, want %v", got, fallback)
	}
}

func TestMonitorPersistsNativeVenueLiquidationPrices(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "native-liquidation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const now = "2026-08-16T12:00:00Z"
	if _, err := database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			account_pacifica, account_hyperliquid, notional, leverage,
			started_at, opened_at, updated_at
		) VALUES ('position-1', 'plan-1', 'opp-1', 'SOL', 'pacifica', 'hyperliquid', 'open',
			'sol-wallet', '0xwallet', 100, 5, ?, ?, ?);
		INSERT INTO live_fills (
			position_id, leg, venue, symbol, side, requested_amount, filled_amount,
			avg_fill_price, fill_ratio, fee, accepted, filled, filled_at
		) VALUES
			('position-1', 1, 'pacifica', 'SOL', 'long', 1, 1, 100, 1, 0, 1, 1, ?),
			('position-1', 2, 'hyperliquid', 'SOL', 'short', 1, 1, 100, 1, 0, 1, 1, ?)
	`, now, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStore(database, logger)
	position, err := store.GetPosition(context.Background(), "position-1")
	if err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(logger, store, monitorMarketSource{}, monitorLiquidationSource{
		"pacifica": 72, "hyperliquid": 131,
	})

	monitor.evaluate(context.Background(), position)

	var leg1Price, leg2Price float64
	if err := database.QueryRow(`
		SELECT leg1_liq_price, leg2_liq_price FROM live_positions WHERE id = 'position-1'
	`).Scan(&leg1Price, &leg2Price); err != nil {
		t.Fatal(err)
	}
	if leg1Price != 72 || leg2Price != 131 {
		t.Fatalf("liquidation prices = %v / %v, want native 72 / 131", leg1Price, leg2Price)
	}
}
