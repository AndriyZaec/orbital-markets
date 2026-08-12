package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestLiveCloseUsesPersistedResidualExposure(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	request := httptest.NewRequest("POST", "/api/v1/live/close/position-residual", jsonBody(t, map[string]string{
		"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
		"agent_pacifica": "sol-agent", "agent_hyperliquid": "0xagent",
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
	if requestToSign.PositionID != "position-residual" || requestToSign.Leg != 1 ||
		requestToSign.Amount != 2.75 || requestToSign.Account != "sol-wallet" ||
		requestToSign.Signer != "sol-agent" {
		t.Fatalf("close request = %+v, want position/leg residual amount 2.75", requestToSign)
	}
}

func TestLiveCloseReconcilesRecordedExposureAbsentFromBothVenues(t *testing.T) {
	server, database := newResidualExposureServer(t)
	registryCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updatedAt := time.Now()
	server.live.accounts = newAccountFeedRegistry(registryCtx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{refreshSnapshots: map[string]liveAccountSnapshot{
			"sol-wallet": {Venue: "pacifica", Account: "sol-wallet", PositionsUpdatedAt: updatedAt},
		}},
		"hyperliquid": &fakeAccountFeedFactory{refreshSnapshots: map[string]liveAccountSnapshot{
			"0xwallet": {Venue: "hyperliquid", Account: "0xwallet", PositionsUpdatedAt: updatedAt},
		}},
	}, accountFeedRegistryConfig{})

	request := httptest.NewRequest("POST", "/api/v1/live/close/position-residual", jsonBody(t, map[string]string{
		"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
	}))
	response := httptest.NewRecorder()
	server.handleLiveClose(response, request)

	if response.Code != 200 {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body struct {
		ReconciledClosed bool                    `json:"reconciled_closed"`
		SigningRequests  []domain.SigningRequest `json:"signing_requests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.ReconciledClosed || len(body.SigningRequests) != 0 {
		t.Fatalf("body = %+v, want venue-reconciled close without signing", body)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM live_positions WHERE id = 'position-residual'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(executor.ExecStateClosed) {
		t.Fatalf("state = %q, want closed", state)
	}
}

func TestLiveCloseReconcilesStaleClosingPositionAbsentFromBothVenues(t *testing.T) {
	server, database := newResidualExposureServer(t)
	if _, err := database.Exec(`UPDATE live_positions SET state = 'closing' WHERE id = 'position-residual'`); err != nil {
		t.Fatal(err)
	}
	registryCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updatedAt := time.Now()
	server.live.accounts = newAccountFeedRegistry(registryCtx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{refreshSnapshots: map[string]liveAccountSnapshot{
			"sol-wallet": {Venue: "pacifica", Account: "sol-wallet", PositionsUpdatedAt: updatedAt},
		}},
		"hyperliquid": &fakeAccountFeedFactory{refreshSnapshots: map[string]liveAccountSnapshot{
			"0xwallet": {Venue: "hyperliquid", Account: "0xwallet", PositionsUpdatedAt: updatedAt},
		}},
	}, accountFeedRegistryConfig{})

	request := httptest.NewRequest("POST", "/api/v1/live/close/position-residual", jsonBody(t, map[string]string{
		"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
	}))
	response := httptest.NewRecorder()
	server.handleLiveClose(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reconciled_closed":true`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestLiveCloseIgnoresFreshMonitorTimestampAfterOldCloseAttempt(t *testing.T) {
	server, database := newResidualExposureServer(t)
	if _, err := database.Exec(`
		UPDATE live_positions SET updated_at = ? WHERE id = 'position-residual';
		INSERT INTO live_close_outcomes (
			position_id, leg, venue, client_order_id, requested_amount,
			resolved, error, created_at, updated_at
		) VALUES ('position-residual', 1, 'pacifica', 'old-close', 2.75, 1, 'rejected', ?, ?)`,
		time.Now().Format(time.RFC3339), "2026-07-22T12:01:00Z", "2026-07-22T12:01:00Z"); err != nil {
		t.Fatal(err)
	}
	registryCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updatedAt := time.Now()
	server.live.accounts = newAccountFeedRegistry(registryCtx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{refreshSnapshots: map[string]liveAccountSnapshot{
			"sol-wallet": {Venue: "pacifica", Account: "sol-wallet", PositionsUpdatedAt: updatedAt},
		}},
		"hyperliquid": &fakeAccountFeedFactory{refreshSnapshots: map[string]liveAccountSnapshot{
			"0xwallet": {Venue: "hyperliquid", Account: "0xwallet", PositionsUpdatedAt: updatedAt},
		}},
	}, accountFeedRegistryConfig{})

	request := httptest.NewRequest("POST", "/api/v1/live/close/position-residual", jsonBody(t, map[string]string{
		"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
	}))
	response := httptest.NewRecorder()
	server.handleLiveClose(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reconciled_closed":true`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestLiveCloseKeepsRecordedExposureWhenVenuePositionExists(t *testing.T) {
	server, database := newResidualExposureServer(t)
	registryCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updatedAt := time.Now()
	server.live.accounts = newAccountFeedRegistry(registryCtx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{
			"sol-wallet": {
				Venue: "pacifica", Account: "sol-wallet", PositionsUpdatedAt: updatedAt,
				Positions: []liveAccountPosition{{Symbol: "SOL", Side: "long", Size: 2.75}},
			},
		}},
		"hyperliquid": &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{
			"0xwallet": {Venue: "hyperliquid", Account: "0xwallet", PositionsUpdatedAt: updatedAt},
		}},
	}, accountFeedRegistryConfig{})

	request := httptest.NewRequest("POST", "/api/v1/live/close/position-residual", jsonBody(t, map[string]string{
		"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
		"agent_pacifica": "sol-agent", "agent_hyperliquid": "0xagent",
	}))
	response := httptest.NewRecorder()
	server.handleLiveClose(response, request)

	if response.Code != 200 {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body struct {
		ReconciledClosed bool                    `json:"reconciled_closed"`
		SigningRequests  []domain.SigningRequest `json:"signing_requests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ReconciledClosed || len(body.SigningRequests) != 1 {
		t.Fatalf("body = %+v, want one close request for venue exposure", body)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM live_positions WHERE id = 'position-residual'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(executor.ExecStateDegraded) {
		t.Fatalf("state = %q, want degraded", state)
	}
}

func TestLiveEventsEmitsInitialAccountSnapshots(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	registryCtx, cancelRegistry := context.WithCancel(context.Background())
	t.Cleanup(cancelRegistry)
	updatedAt := time.Now()
	server.live.accounts = newAccountFeedRegistry(registryCtx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{
			"sol-wallet": {Venue: "pacifica", Account: "sol-wallet", PositionsUpdatedAt: updatedAt},
		}},
		"hyperliquid": &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{
			"0xwallet": {Venue: "hyperliquid", Account: "0xwallet", PositionsUpdatedAt: updatedAt},
		}},
	}, accountFeedRegistryConfig{})

	requestCtx, cancelRequest := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelRequest()
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/live/events?account_pacifica=sol-wallet&account_hyperliquid=0xwallet", nil,
	).WithContext(requestCtx)
	response := httptest.NewRecorder()
	server.handleLiveEvents(response, request)

	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type = %q", response.Header().Get("Content-Type"))
	}
	body := response.Body.String()
	if !strings.Contains(body, "event: balances\n") || !strings.Contains(body, "event: positions\n") {
		t.Fatalf("stream body = %q, want initial balances and positions", body)
	}
}

func TestKillSwitchReturnsExactRemainingExposure(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	request := httptest.NewRequest("POST", "/api/v1/live/kill", jsonBody(t, map[string]string{
		"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
		"agent_pacifica": "sol-agent", "agent_hyperliquid": "0xagent",
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

func TestLiveCloseDoesNotExposeAnotherAccountPosition(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	request := httptest.NewRequest("POST", "/api/v1/live/close/position-residual", jsonBody(t, map[string]string{
		"account_pacifica": "other-wallet", "account_hyperliquid": "0xother",
		"agent_pacifica": "other-agent", "agent_hyperliquid": "0xotheragent",
	}))
	response := httptest.NewRecorder()

	server.handleLiveClose(response, request)
	if response.Code != 404 {
		t.Fatalf("status = %d body = %s, want not found", response.Code, response.Body.String())
	}
}

func TestKillSwitchTargetsOnlyMatchingAccountPositions(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	request := httptest.NewRequest("POST", "/api/v1/live/kill", jsonBody(t, map[string]string{
		"account_pacifica": "other-wallet", "account_hyperliquid": "0xother",
		"agent_pacifica": "other-agent", "agent_hyperliquid": "0xotheragent",
	}))
	response := httptest.NewRecorder()

	server.handleLiveKill(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Targeted int `json:"targeted"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Targeted != 0 {
		t.Fatalf("targeted = %d, want 0", body.Targeted)
	}
}

func TestLiveSessionStatusReturnsTerminalRecoveryOutcome(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	now := time.Now()
	payload, err := marshalLiveSession(&LiveSession{
		ID: "session-recovered", Plan: &domain.ExecutionPlan{ID: "position-residual"},
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.liveStore.UpsertDurableSession(context.Background(), executor.DurableSessionRecord{
		ID: "session-recovered", State: string(sessRecovering), Payload: payload,
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet", Asset: "SOL",
		HasExposure: true, ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.liveStore.FinishDurableSession(context.Background(), "session-recovered", string(sessDegraded), "unwind unconfirmed"); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		"GET",
		"/api/v1/live/sessions/session-recovered?account_pacifica=sol-wallet&account_hyperliquid=0xwallet",
		nil,
	)
	response := httptest.NewRecorder()
	server.handleLiveSessionStatus(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Status            string `json:"status"`
		Reason            string `json:"reason"`
		PositionID        string `json:"position_id"`
		RemainingExposure []struct {
			Amount float64 `json:"amount"`
		} `json:"remaining_exposure"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != string(sessDegraded) || body.Reason != "unwind unconfirmed" || body.PositionID != "position-residual" {
		t.Fatalf("session status = %+v", body)
	}
	if len(body.RemainingExposure) != 1 || body.RemainingExposure[0].Amount != 2.75 {
		t.Fatalf("remaining exposure = %+v, want 2.75", body.RemainingExposure)
	}
}

func TestLiveSessionStatusRejectsAnotherAccountPair(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	now := time.Now()
	payload, err := marshalLiveSession(&LiveSession{
		ID: "session-private", Plan: &domain.ExecutionPlan{ID: "position-residual"},
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.liveStore.UpsertDurableSession(context.Background(), executor.DurableSessionRecord{
		ID: "session-private", State: string(sessRecovering), Payload: payload,
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet", Asset: "SOL",
		HasExposure: true, ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		"GET",
		"/api/v1/live/sessions/session-private?account_pacifica=other&account_hyperliquid=0xother",
		nil,
	)
	response := httptest.NewRecorder()
	server.handleLiveSessionStatus(response, request)
	if response.Code != 404 {
		t.Fatalf("status = %d body = %s, want not found", response.Code, response.Body.String())
	}
}

func TestLiveSessionStatusKeepsActiveRequestsRecovering(t *testing.T) {
	if status := publicLiveSessionStatus(string(sessAwaitingLeg1Signs), false); status != string(sessRecovering) {
		t.Fatalf("status = %q, want recovering", status)
	}
}

func TestLivePrepareConflictDoesNotExposeActiveSessionID(t *testing.T) {
	responseBody := liveSessionConflictResponse("trade still being checked", string(sessRecovering))
	if _, exposed := responseBody["session_id"]; exposed {
		t.Fatalf("conflict response exposed active session: %+v", responseBody)
	}
}

func TestAmbiguousCloseSubmissionRemainsPending(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	recorded := server.recordAmbiguousCloseSubmission(&domain.SigningRequest{
		PositionID: "position-residual", Leg: 1, Venue: "pacifica", Symbol: "SOL",
		ClientOrderID: "close-uncertain", Amount: 2.75,
	}, "connection timed out")
	if !recorded {
		t.Fatal("ambiguous close submission was not recorded")
	}
	progress, err := server.liveStore.GetCloseProgress(context.Background(), "position-residual")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Pending != 1 || progress.Failed != 0 {
		t.Fatalf("close progress = %+v, want one pending outcome", progress)
	}
	position, err := server.liveStore.GetPosition(context.Background(), "position-residual")
	if err != nil {
		t.Fatal(err)
	}
	if position.State != string(executor.ExecStateClosing) {
		t.Fatalf("position state = %q, want closing", position.State)
	}
}

func TestWrongCloseSignerIsRejectedBeforeReconciliation(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	requestToSign := &domain.SigningRequest{
		ID: "close-request", ClientOrderID: "close-order", PositionID: "position-residual",
		Leg: 1, Venue: "pacifica", Account: "sol-wallet", Action: "close", ReduceOnly: true,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	server.live.signingStore.Store(requestToSign)
	request := httptest.NewRequest("POST", "/api/v1/live/submit", jsonBody(t, domain.SignedAction{
		RequestID: requestToSign.ID, ClientOrderID: requestToSign.ClientOrderID,
		Venue: "pacifica", SignerAddress: "wrong-wallet", Signature: "signature",
	}))
	response := httptest.NewRecorder()
	server.handleLiveSubmit(response, request)
	if response.Code != 400 {
		t.Fatalf("status = %d body = %s, want bad request", response.Code, response.Body.String())
	}
	position, err := server.liveStore.GetPosition(context.Background(), "position-residual")
	if err != nil {
		t.Fatal(err)
	}
	if position.State != string(executor.ExecStateDegraded) {
		t.Fatalf("position state = %q, wrong signer started reconciliation", position.State)
	}
}

func TestCloseCapacityFailureDoesNotStartReconciliation(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.live.accounts = newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"pacifica": &fakeAccountFeedFactory{},
	}, accountFeedRegistryConfig{MaxFeeds: 1, MaxPerVenue: 1})
	occupied, err := server.live.accounts.Acquire("pacifica", "occupied-wallet")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Release()

	requestToSign := &domain.SigningRequest{
		ID: "close-capacity", ClientOrderID: "close-capacity-order", PositionID: "position-residual",
		Leg: 1, Venue: "pacifica", Account: "sol-wallet", Action: "close", ReduceOnly: true,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	server.live.signingStore.Store(requestToSign)
	signed := domain.SignedAction{
		RequestID: requestToSign.ID, ClientOrderID: requestToSign.ClientOrderID,
		Venue: "pacifica", SignerAddress: "sol-wallet", Signature: "signature",
	}
	request := httptest.NewRequest("POST", "/api/v1/live/submit", jsonBody(t, signed))
	response := httptest.NewRecorder()
	server.handleLiveSubmit(response, request)
	if response.Code != 503 {
		t.Fatalf("status = %d body = %s, want service unavailable", response.Code, response.Body.String())
	}
	position, err := server.liveStore.GetPosition(context.Background(), "position-residual")
	if err != nil {
		t.Fatal(err)
	}
	if position.State != string(executor.ExecStateDegraded) {
		t.Fatalf("position state = %q, local capacity failure started reconciliation", position.State)
	}
	if _, err := server.live.signingStore.ValidateAndConsume(signed); err != nil {
		t.Fatalf("unsent signing request was not restored: %v", err)
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
			account_pacifica, account_hyperliquid,
			notional, leverage, started_at, opened_at, completed_at, updated_at
		) VALUES (
			'position-residual', 'plan-1', 'opportunity-1', 'SOL', 'pacifica', 'hyperliquid', 'degraded',
			'sol-wallet', '0xwallet',
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
	live := &LiveDeps{
		liveStore: liveStore, signingStore: domain.NewSigningRequestStore(),
		pacificaLotSizes: recoveryTestLotSizes{},
	}
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
