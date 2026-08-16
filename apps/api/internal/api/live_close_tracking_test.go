package api

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestConfirmedCloseFillsKeepResidualVenueExposureCloseable(t *testing.T) {
	server, _ := newCloseConfirmationServer(t,
		[]liveAccountPosition{{Symbol: "SOL", Side: "long", Size: 0.004}},
		[]liveAccountPosition{{Symbol: "SOL", Side: "short", Size: 0.004}},
		nil,
	)
	confirmBothCloseFills(server)
	assertPositionState(t, server, executor.ExecStateDegraded)
}

func TestConfirmedCloseFillsClosePositionWhenVenuesAreFlat(t *testing.T) {
	server, _ := newCloseConfirmationServer(t, nil, nil, nil)
	confirmBothCloseFills(server)
	assertPositionState(t, server, executor.ExecStateClosed)
}

func TestConfirmedCloseFillsStayClosingWhenVenueStateIsUnavailable(t *testing.T) {
	server, database := newCloseConfirmationServer(t, nil, nil, errors.New("venue unavailable"))
	confirmBothCloseFills(server)
	assertPositionState(t, server, executor.ExecStateClosing)

	if _, err := database.Exec(`UPDATE live_close_outcomes SET updated_at = '2026-07-22T12:01:00Z'`); err != nil {
		t.Fatal(err)
	}
	setCloseConfirmationAccounts(t, server,
		[]liveAccountPosition{{Symbol: "SOL", Side: "long", Size: 0.004}},
		[]liveAccountPosition{{Symbol: "SOL", Side: "short", Size: 0.004}},
		nil,
	)
	server.reconcileClosingPositions()
	assertPositionState(t, server, executor.ExecStateDegraded)
}

func newCloseConfirmationServer(
	t *testing.T,
	pacificaPositions, hyperliquidPositions []liveAccountPosition,
	pacificaRefreshErr error,
) (*Server, *sql.DB) {
	t.Helper()
	server, database := newResidualExposureServer(t)
	const persistedAt = "2026-07-22T12:00:00Z"
	if _, err := database.Exec(`
		UPDATE live_positions SET state = 'closing' WHERE id = 'position-residual';
		INSERT INTO live_fills (
			position_id, leg, venue, symbol, side, order_id, client_order_id,
			requested_amount, filled_amount, avg_fill_price, fill_ratio, fee,
			accepted, filled, error, filled_at
		) VALUES (
			'position-residual', 2, 'hyperliquid', 'SOL', 'short', 'order-2', 'client-2',
			1, 1, 100, 1, 0, 1, 1, '', ?
		)`, persistedAt); err != nil {
		t.Fatal(err)
	}
	setCloseConfirmationAccounts(t, server, pacificaPositions, hyperliquidPositions, pacificaRefreshErr)
	return server, database
}

func setCloseConfirmationAccounts(
	t *testing.T,
	server *Server,
	pacificaPositions, hyperliquidPositions []liveAccountPosition,
	pacificaRefreshErr error,
) {
	t.Helper()
	registryCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updatedAt := time.Now()
	server.live.accounts = newAccountFeedRegistry(registryCtx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{
			refreshErr: pacificaRefreshErr,
			refreshSnapshots: map[string]liveAccountSnapshot{"sol-wallet": {
				Venue: "pacifica", Account: "sol-wallet", PositionsUpdatedAt: updatedAt,
				Positions: pacificaPositions,
			}},
			waitFills: map[string]*normFill{"sol-wallet": {
				Filled: true, FilledAmount: 0.996, AvgFillPrice: 101, OrderID: "close-1",
			}},
		},
		"hyperliquid": &fakeAccountFeedFactory{
			refreshSnapshots: map[string]liveAccountSnapshot{"0xwallet": {
				Venue: "hyperliquid", Account: "0xwallet", PositionsUpdatedAt: updatedAt,
				Positions: hyperliquidPositions,
			}},
			waitFills: map[string]*normFill{"0xwallet": {
				Filled: true, FilledAmount: 0.996, AvgFillPrice: 99, OrderID: "close-2",
			}},
		},
	}, accountFeedRegistryConfig{})
}

func confirmBothCloseFills(server *Server) {
	createdAt := time.Now().Add(-time.Second)
	server.awaitCloseFill(&domain.SigningRequest{
		PositionID: "position-residual", Leg: 1, Venue: "pacifica", Account: "sol-wallet",
		ClientOrderID: "close-1", Amount: 1, CreatedAt: createdAt,
	}, "close-1")
	server.awaitCloseFill(&domain.SigningRequest{
		PositionID: "position-residual", Leg: 2, Venue: "hyperliquid", Account: "0xwallet",
		ClientOrderID: "close-2", Amount: 1, CreatedAt: createdAt,
	}, "close-2")
}

func assertPositionState(t *testing.T, server *Server, want executor.ExecState) {
	t.Helper()
	position, err := server.liveStore.GetPosition(context.Background(), "position-residual")
	if err != nil {
		t.Fatal(err)
	}
	if position.State != string(want) {
		t.Fatalf("position state = %q, want %q", position.State, want)
	}
}
