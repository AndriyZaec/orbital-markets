package api

import (
	"context"
	"testing"
	"time"
)

func TestLiquidationPricesUseFreshVenueAccountPositions(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	setLiquidationTestAccounts(t, server, time.Now())
	position, err := server.liveStore.GetPosition(context.Background(), "position-residual")
	if err != nil {
		t.Fatal(err)
	}

	prices, err := server.live.LiquidationPrices(context.Background(), position)
	if err != nil {
		t.Fatal(err)
	}
	if prices["pacifica"] != 72 || prices["hyperliquid"] != 131 {
		t.Fatalf("liquidation prices = %+v, want Pacifica 72 and Hyperliquid 131", prices)
	}
}

func TestLiquidationPricesIgnoreStaleAccountPositions(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	setLiquidationTestAccounts(t, server, time.Now().Add(-admissionFreshness-time.Second))
	position, err := server.liveStore.GetPosition(context.Background(), "position-residual")
	if err != nil {
		t.Fatal(err)
	}

	prices, err := server.live.LiquidationPrices(context.Background(), position)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 0 {
		t.Fatalf("stale liquidation prices = %+v, want approximation fallback", prices)
	}
}

func setLiquidationTestAccounts(t *testing.T, server *Server, updatedAt time.Time) {
	t.Helper()
	registryCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server.live.accounts = newAccountFeedRegistry(registryCtx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{
			"sol-wallet": {
				Venue: "pacifica", Account: "sol-wallet", PositionsUpdatedAt: updatedAt,
				Positions: []liveAccountPosition{{Symbol: "SOL", LiqPrice: 72}},
			},
		}},
		"hyperliquid": &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{
			"0xwallet": {
				Venue: "hyperliquid", Account: "0xwallet", PositionsUpdatedAt: updatedAt,
				Positions: []liveAccountPosition{{Symbol: "SOL", LiqPrice: 131}},
			},
		}},
	}, accountFeedRegistryConfig{})
}
