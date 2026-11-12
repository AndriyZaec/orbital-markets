package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func newPendingFundingServer(t *testing.T) (*Server, *sql.DB, *fakeAccountFeed) {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "pending-funding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := executor.NewStore(db, db, logger)
	opened := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	bindings := map[string]string{"phoenix": phoenixTestOwner, "hyperliquid": "0xwallet"}
	result := &executor.ExecutionResult{PlanID: "pending-position", Asset: "SOL", State: executor.ExecStateOpen, StartedAt: opened,
		Leg1: executor.LegResult{Venue: "phoenix", Symbol: "SOL-PERP", Side: "long", Submitted: true, Accepted: true, Filled: true, FilledAmount: 1, AvgFillPrice: 100}}
	if err := store.PersistFullResultAtomicForBindings(ctx, result, "phoenix", "hyperliquid", bindings, 100, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE live_positions SET opened_at=?,price_pnl=10 WHERE id=?`, opened.Format(time.RFC3339), result.PlanID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	factory := &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{phoenixTestOwner: {
		Venue: "phoenix", Account: phoenixTestOwner, Connected: true, LastUpdated: now, PositionsUpdatedAt: now,
		Positions: []liveAccountPosition{{Symbol: "SOL-PERP", Side: "long", Size: 1, PendingFundingQuoteLots: "2000000"}},
	}}}
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{"phoenix": factory}, accountFeedRegistryConfig{})
	lease, err := registry.Acquire("phoenix", phoenixTestOwner)
	if err != nil {
		t.Fatal(err)
	}
	feed := lease.Feed().(*fakeAccountFeed)
	lease.Release()
	server := &Server{ctx: ctx, liveStore: store, logger: logger, live: &LiveDeps{accounts: registry, liveStore: store}}
	window := executor.FundingWindow{Since: opened, Until: now}
	for _, name := range []string{"phoenix", "hyperliquid"} {
		var payments []venue.FundingPayment
		if name == "hyperliquid" {
			payments = []venue.FundingPayment{{ExternalID: "hl-payment", Venue: name, Account: bindings[name], Asset: "SOL", AmountUSD: 4, PaidAt: opened.Add(time.Hour)}}
		}
		if err := store.ApplyFundingWindow(ctx, result.PlanID, name, bindings[name], "SOL", opened, window, payments); err != nil {
			t.Fatal(err)
		}
	}
	position, err := store.GetPosition(ctx, result.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if total, complete, err := store.ReconcileFunding(ctx, position, now, false); err != nil || !complete || total != 4 {
		t.Fatal(total, complete, err)
	}
	return server, db, feed
}

func pendingFundingResponse(t *testing.T, server *Server, detail bool) executor.LivePosition {
	t.Helper()
	query := url.Values{"accounts[phoenix]": {phoenixTestOwner}, "accounts[hyperliquid]": {"0xwallet"}}
	path := "/api/v1/live/positions"
	if detail {
		path += "/pending-position"
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path+"?"+query.Encode(), nil)
	if detail {
		server.handleLivePosition(recorder, request)
	} else {
		server.handleLivePositions(recorder, request)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d: %s", recorder.Code, recorder.Body.String())
	}
	if detail {
		var response struct {
			Position executor.LivePosition `json:"position"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Position
	}
	var positions []executor.LivePosition
	if err := json.Unmarshal(recorder.Body.Bytes(), &positions); err != nil || len(positions) != 1 {
		t.Fatal(recorder.Body.String(), err)
	}
	return positions[0]
}

func TestPhoenixPendingFundingLiveResponsesDoNotPersistOrDoubleCount(t *testing.T) {
	server, _, feed := newPendingFundingServer(t)
	ctx := context.Background()
	for range 2 {
		for _, detail := range []bool{false, true} {
			position := pendingFundingResponse(t, server, detail)
			pending := position.PhoenixPendingFunding
			if pending == nil || pending.AmountUSD == nil || *pending.AmountUSD != 2 || pending.AsOf == nil || pending.UnavailableReason != "" || position.FundingPnL != 4 || position.TotalPnL != 16 {
				t.Fatalf("incorrect live funding: %+v, pending %+v", position, pending)
			}
		}
	}
	persisted, err := server.liveStore.GetPosition(ctx, "pending-position")
	if err != nil || persisted.TotalPnL != 14 || persisted.FundingPnL != 4 || persisted.PhoenixPendingFunding != nil {
		t.Fatal("pending changed cash ledger", persisted, err)
	}

	// Settlement moves the same $2 from pending to the cash ledger.
	opened, _ := time.Parse(time.RFC3339, persisted.OpenedAt)
	paidAt := time.Now().UTC().Truncate(time.Millisecond)
	window := executor.FundingWindow{Since: opened, Until: paidAt}
	payment := venue.FundingPayment{ExternalID: "phx-settlement", Venue: "phoenix", Account: phoenixTestOwner, Asset: "SOL", MarketKey: "SOL-PERP", AmountUSD: 2, PaidAt: paidAt, PositionSize: "1", PositionSide: "long"}
	if err := server.liveStore.ApplyFundingWindow(ctx, persisted.ID, "phoenix", phoenixTestOwner, "SOL", opened, window, []venue.FundingPayment{payment}); err != nil {
		t.Fatal(err)
	}
	if err := server.liveStore.ApplyFundingWindow(ctx, persisted.ID, "hyperliquid", "0xwallet", "SOL", opened, window, nil); err != nil {
		t.Fatal(err)
	}
	if total, complete, err := server.liveStore.ReconcileFunding(ctx, persisted, paidAt, false); err != nil || !complete || total != 6 {
		t.Fatal(total, complete, err)
	}
	feed.mu.Lock()
	feed.snapshot.PositionsUpdatedAt = paidAt.Add(-time.Second)
	feed.mu.Unlock()
	lagging := pendingFundingResponse(t, server, false)
	if lagging.PhoenixPendingFunding.AmountUSD != nil || lagging.TotalPnL != 16 {
		t.Fatalf("pre-settlement snapshot was added to settled cash: %+v", lagging)
	}
	feed.mu.Lock()
	feed.snapshot.LastUpdated = paidAt
	feed.snapshot.PositionsUpdatedAt = paidAt
	feed.snapshot.Positions[0].PendingFundingQuoteLots = "0"
	feed.mu.Unlock()
	for _, detail := range []bool{false, true} {
		position := pendingFundingResponse(t, server, detail)
		if position.PhoenixPendingFunding == nil || position.PhoenixPendingFunding.AmountUSD == nil || *position.PhoenixPendingFunding.AmountUSD != 0 || position.FundingPnL != 6 || position.TotalPnL != 16 {
			t.Fatalf("settlement counted twice: %+v", position)
		}
	}
}

func TestPhoenixPendingFundingUnavailableIsNotZero(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*liveAccountSnapshot)
	}{
		{"shared exposure", func(s *liveAccountSnapshot) { s.Positions[0].Size = 2 }},
		{"opposite side", func(s *liveAccountSnapshot) { s.Positions[0].Side = "short" }},
		{"native market mismatch", func(s *liveAccountSnapshot) { s.Positions[0].Symbol = "SOL" }},
		{"missing position", func(s *liveAccountSnapshot) { s.Positions = nil }},
		{"stale state", func(s *liveAccountSnapshot) { s.PositionsUpdatedAt = time.Now().Add(-admissionFreshness - time.Second) }},
		{"stream gap", func(s *liveAccountSnapshot) { s.UnavailableReason = "sequence gap" }},
		{"disconnected", func(s *liveAccountSnapshot) { s.Connected = false }},
		{"missing funding", func(s *liveAccountSnapshot) { s.Positions[0].PendingFundingQuoteLots = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, feed := newPendingFundingServer(t)
			feed.mu.Lock()
			tc.change(&feed.snapshot)
			feed.mu.Unlock()
			position := pendingFundingResponse(t, server, false)
			if position.PhoenixPendingFunding == nil || position.PhoenixPendingFunding.AmountUSD != nil || position.PhoenixPendingFunding.UnavailableReason == "" || position.TotalPnL != 14 {
				t.Fatalf("unavailable funding reported as known: %+v", position)
			}
		})
	}
}

func TestPhoenixPendingFundingDoesNotAddToEstimatedOrClosedPnL(t *testing.T) {
	server, db, feed := newPendingFundingServer(t)
	if _, err := db.Exec(`UPDATE live_positions SET funding_pnl_source='estimated' WHERE id='pending-position'`); err != nil {
		t.Fatal(err)
	}
	position := pendingFundingResponse(t, server, false)
	if position.PhoenixPendingFunding == nil || position.PhoenixPendingFunding.AmountUSD != nil || position.TotalPnL != 14 {
		t.Fatalf("pending added to rate estimate: %+v", position)
	}
	if _, err := db.Exec(`UPDATE live_positions SET funding_pnl_source='realized' WHERE id='pending-position'`); err != nil {
		t.Fatal(err)
	}
	feed.mu.Lock()
	feed.snapshot.Positions[0].PendingFundingQuoteLots = "-1500000"
	feed.mu.Unlock()
	position = pendingFundingResponse(t, server, false)
	if position.TotalPnL != 12.5 || position.PhoenixPendingFunding.AmountUSD == nil || *position.PhoenixPendingFunding.AmountUSD != -1.5 {
		t.Fatalf("negative pending lost: %+v", position)
	}
	if _, err := db.Exec(`UPDATE live_positions SET state='closed' WHERE id='pending-position'`); err != nil {
		t.Fatal(err)
	}
	position = pendingFundingResponse(t, server, true)
	if position.PhoenixPendingFunding != nil || position.TotalPnL != 14 {
		t.Fatalf("pending changed closed PnL: %+v", position)
	}
}
