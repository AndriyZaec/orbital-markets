package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestOpenPositionReconciliationUsesFreshCachedExposureWithoutRefreshing(t *testing.T) {
	server := newOpenReconciliationServer(t,
		[]liveAccountPosition{{Symbol: "SOL", Side: "long", Size: 2.75}}, nil,
		time.Now(), time.Now().Add(-time.Hour),
	)
	server.reconcileOpenPositions()
	assertPositionState(t, server, executor.ExecStateDegraded)
}

func TestOpenPositionReconciliationKeepsMatchingExposureOpen(t *testing.T) {
	server := newOpenReconciliationServer(t,
		[]liveAccountPosition{{Symbol: "SOL", Side: "long", Size: 2.75}},
		[]liveAccountPosition{{Symbol: "SOL", Side: "short", Size: 2.75}},
		time.Now(), time.Now().Add(-time.Hour),
	)
	server.reconcileOpenPositions()
	assertPositionState(t, server, executor.ExecStateOpen)
}

func TestOpenPositionReconciliationClosesFreshFlatExposure(t *testing.T) {
	server := newOpenReconciliationServer(t, nil, nil, time.Now(), time.Now().Add(-time.Hour))
	server.reconcileOpenPositions()
	assertPositionState(t, server, executor.ExecStateClosed)
}

func TestOpenPositionReconciliationIgnoresSnapshotPredatingOpen(t *testing.T) {
	openedAt := time.Now()
	server := newOpenReconciliationServer(t, nil, nil, openedAt.Add(-time.Second), openedAt)
	server.reconcileOpenPositions()
	assertPositionState(t, server, executor.ExecStateOpen)
}

func newOpenReconciliationServer(
	t *testing.T,
	pacificaPositions, hyperliquidPositions []liveAccountPosition,
	updatedAt, openedAt time.Time,
) *Server {
	t.Helper()
	server, database := newResidualExposureServer(t)
	if _, err := database.Exec(`
		UPDATE live_positions SET state = 'open', opened_at = ? WHERE id = 'position-residual';
		INSERT INTO live_fills (
			position_id, leg, venue, symbol, side, requested_amount, filled_amount,
			avg_fill_price, fill_ratio, fee, accepted, filled, filled_at
		) VALUES ('position-residual', 2, 'hyperliquid', 'SOL', 'short', 2.75, 2.75,
			100, 1, 0, 1, 1, '2026-07-22T12:00:00Z')
	`, openedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	registryCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server.live.accounts = newAccountFeedRegistry(registryCtx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{
			refreshErr: errors.New("background reconciliation must not refresh"),
			snapshots: map[string]liveAccountSnapshot{"sol-wallet": {
				Venue: "pacifica", Account: "sol-wallet", PositionsUpdatedAt: updatedAt,
				Positions: pacificaPositions,
			}},
		},
		"hyperliquid": &fakeAccountFeedFactory{
			refreshErr: errors.New("background reconciliation must not refresh"),
			snapshots: map[string]liveAccountSnapshot{"0xwallet": {
				Venue: "hyperliquid", Account: "0xwallet", PositionsUpdatedAt: updatedAt,
				Positions: hyperliquidPositions,
			}},
		},
	}, accountFeedRegistryConfig{})

	return server
}
