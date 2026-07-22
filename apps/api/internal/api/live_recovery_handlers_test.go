package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestLiveCloseUsesPersistedResidualExposure(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	request := httptest.NewRequest("POST", "/api/v1/live/close/position-residual", jsonBody(t, map[string]string{
		"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
	}))
	response := httptest.NewRecorder()

	server.handleLiveClose(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body struct {
		SigningRequests []domain.SigningRequest `json:"signing_requests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.SigningRequests) != 1 {
		t.Fatalf("signing requests = %d, want 1", len(body.SigningRequests))
	}
	requestToSign := body.SigningRequests[0]
	if requestToSign.PositionID != "position-residual" || requestToSign.Leg != 1 || requestToSign.Amount != 2.75 {
		t.Fatalf("close request = %+v, want position/leg residual amount 2.75", requestToSign)
	}
}

func TestKillSwitchReturnsExactRemainingExposure(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	request := httptest.NewRequest("POST", "/api/v1/live/kill", jsonBody(t, map[string]string{
		"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
	}))
	response := httptest.NewRecorder()

	server.handleLiveKill(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Positions []struct {
			Exposure []struct {
				Leg    int     `json:"leg"`
				Venue  string  `json:"venue"`
				Amount float64 `json:"amount"`
			} `json:"remaining_exposure"`
		} `json:"positions"`
		SigningRequests []domain.SigningRequest `json:"signing_requests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Positions) != 1 || len(body.Positions[0].Exposure) != 1 {
		t.Fatalf("positions = %+v, want one residual exposure", body.Positions)
	}
	exposure := body.Positions[0].Exposure[0]
	if exposure.Leg != 1 || exposure.Venue != "pacifica" || exposure.Amount != 2.75 {
		t.Fatalf("exposure = %+v, want Pacifica leg 1 amount 2.75", exposure)
	}
	if len(body.SigningRequests) != 1 || body.SigningRequests[0].Amount != 2.75 {
		t.Fatalf("signing requests = %+v, want residual amount 2.75", body.SigningRequests)
	}
}

func newResidualExposureServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	database, err := appdb.Open(filepath.Join(t.TempDir(), "residual.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	const now = "2026-07-22T12:00:00Z"
	_, err = database.Exec(`
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			notional, leverage, started_at, opened_at, completed_at, updated_at
		) VALUES (
			'position-residual', 'plan-1', 'opportunity-1', 'SOL', 'pacifica', 'hyperliquid', 'degraded',
			10, 2, ?, ?, ?, ?
		)`, now, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		INSERT INTO live_fills (
			position_id, leg, venue, symbol, side, order_id, client_order_id,
			requested_amount, filled_amount, avg_fill_price, fill_ratio, fee,
			accepted, filled, error, filled_at
		) VALUES (
			'position-residual', 1, 'pacifica', 'SOL', 'long', 'order-1', 'client-1',
			10, 2.75, 100, 0.275, 0, 1, 1, '', ?
		)`, now)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	liveStore := executor.NewStore(database, logger)
	live := &LiveDeps{liveStore: liveStore, signingStore: domain.NewSigningRequestStore()}
	return &Server{ctx: context.Background(), liveStore: liveStore, live: live, logger: logger}, database
}

func jsonBody(t *testing.T, value any) *bytes.Reader {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(data)
}
