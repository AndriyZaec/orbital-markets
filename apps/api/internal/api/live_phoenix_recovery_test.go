package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"
)

type phoenixRecoveryTestFeed struct {
	*fakeAccountFeed
	recoveryMu        sync.Mutex
	positions         []liveAccountPosition
	exact             map[string]*normFill
	lookupErr         error
	refreshErr        error
	positionsAt       *time.Time
	pending           bool
	closeRequest      *domain.SigningRequest
	closeFill         *normFill
	sends             atomic.Int32
	refreshesRecovery atomic.Int32
}

func newRecoveryTestFeed() *phoenixRecoveryTestFeed {
	return &phoenixRecoveryTestFeed{fakeAccountFeed: &fakeAccountFeed{}, exact: map[string]*normFill{}}
}
func (f *phoenixRecoveryTestFeed) RecoverySnapshot() liveAccountSnapshot {
	f.recoveryMu.Lock()
	defer f.recoveryMu.Unlock()
	updatedAt := time.Now().UTC()
	if f.positionsAt != nil {
		updatedAt = *f.positionsAt
	}
	return liveAccountSnapshot{Venue: "phoenix", Account: "phi-owner", Connected: true, PositionsUpdatedAt: updatedAt, Positions: append([]liveAccountPosition(nil), f.positions...)}
}
func (f *phoenixRecoveryTestFeed) RefreshRecoveryPositions(context.Context) error {
	f.refreshesRecovery.Add(1)
	return f.refreshErr
}

func (f *phoenixRecoveryTestFeed) ReconcileOutstandingOrders(context.Context) error {
	f.recoveryMu.Lock()
	defer f.recoveryMu.Unlock()
	if f.lookupErr == nil {
		f.pending = false
	}
	return f.lookupErr
}
func (f *phoenixRecoveryTestFeed) HasUnresolvedOrders(context.Context) (bool, error) {
	f.recoveryMu.Lock()
	defer f.recoveryMu.Unlock()
	return f.pending, nil
}
func (f *phoenixRecoveryTestFeed) ReconcileOrder(_ context.Context, q *domain.SigningRequest) (*normFill, error) {
	f.recoveryMu.Lock()
	defer f.recoveryMu.Unlock()
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	if fill := f.exact[q.ID]; fill != nil {
		copy := *fill
		return &copy, nil
	}
	return &normFill{Status: "not_submitted"}, nil
}
func (f *phoenixRecoveryTestFeed) SubmitSigned(_ context.Context, _ domain.SignedAction, q *domain.SigningRequest) (*domain.SubmissionResult, error) {
	f.sends.Add(1)
	f.recoveryMu.Lock()
	defer f.recoveryMu.Unlock()
	f.exact[q.ID] = &normFill{Filled: true, FilledAmount: q.Amount, AvgFillPrice: 100, OrderID: "local-transaction", Status: "filled"}
	return &domain.SubmissionResult{Accepted: true, OrderID: "local-transaction"}, nil
}
func (f *phoenixRecoveryTestFeed) WaitForFill(ctx context.Context, q *domain.SigningRequest) (*normFill, error) {
	return f.ReconcileOrder(ctx, q)
}
func (f *phoenixRecoveryTestFeed) ReconcileClose(context.Context, string) (*domain.SigningRequest, *normFill, error) {
	f.recoveryMu.Lock()
	defer f.recoveryMu.Unlock()
	return f.closeRequest, f.closeFill, f.lookupErr
}

func recoveryPhoenixContext(t *testing.T, feed *phoenixRecoveryTestFeed) (*Server, *liveAccountContext) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{"phoenix": &fixedAccountFeedFactory{feed: feed}, "hyperliquid": &fixedAccountFeedFactory{feed: &fakeAccountFeed{refreshSnapshot: &liveAccountSnapshot{PositionsUpdatedAt: time.Now().UTC()}}}}, accountFeedRegistryConfig{})
	live := &LiveDeps{accounts: registry}
	accounts, err := live.acquireAccountContext(map[string]string{"phoenix": "phi-owner", "hyperliquid": "0xwallet"}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(accounts.Release)
	return &Server{ctx: ctx, live: live, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, accounts
}

func TestPhoenixRecoveryExposureDoesNotDependOnRiskOrDelegationReadiness(t *testing.T) {
	f := newPhoenixAccountFixture(t)
	lease, feed := f.ready(t)
	defer lease.Release()
	f.backend.mu.Lock()
	f.backend.riskErr = errors.New("collateral pricing unavailable")
	f.backend.authorityErr = errors.New("authority unavailable")
	f.backend.mu.Unlock()
	if err := feed.refreshView(context.Background()); err == nil {
		t.Fatal("risk view did not fail")
	}
	if !feed.Snapshot().PositionsUpdatedAt.IsZero() {
		t.Fatal("admission snapshot stayed usable")
	}
	if err := feed.RefreshRecoveryPositions(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := feed.RecoverySnapshot()
	if view.PositionsUpdatedAt.IsZero() || len(view.Positions) != 1 || view.Positions[0].BaseLots != "-10" || view.Positions[0].Size != .001 {
		t.Fatalf("actual exposure was hidden by risk failure: %+v", view)
	}
	f.markets.disconnected.Store(true)
	if view := feed.RecoverySnapshot(); !view.PositionsUpdatedAt.IsZero() {
		t.Fatal("unknown position precision was inferred as fresh truth")
	}
	f.markets.disconnected.Store(false)
	f.send <- map[string]any{"channel": "traderState", "authority": phoenixTestOwner, "traderPdaIndex": 0, "slot": 101, "messageType": "delta", "deltas": []any{map[string]any{"subaccountIndex": 0, "sequence": 43, "collateral": "100000000"}}}
	waitPhoenix(t, func() bool { return !feed.state.Snapshot().Synchronized })
	if view := feed.RecoverySnapshot(); !view.PositionsUpdatedAt.IsZero() {
		t.Fatal("stream gap was relabeled as fresh recovery truth")
	}
}

func TestPhoenixFlatSnapshotCannotFinalizeWhileTransactionMayLand(t *testing.T) {
	feed := newRecoveryTestFeed()
	feed.pending = true
	server, accounts := recoveryPhoenixContext(t, feed)
	if !venuePositionStateReadyAfter(accounts, "phoenix", time.Now().Add(-time.Second)) {
		t.Fatal("raw recovery source was not used")
	}
	if pending, err := recoveryOrdersUnresolved(context.Background(), accounts); err != nil || !pending {
		t.Fatal(pending, err)
	}
	// Ordinary risk/admission Snapshot intentionally has no freshness timestamp.
	if !feed.Snapshot().PositionsUpdatedAt.IsZero() {
		t.Fatal("fixture admission snapshot unexpectedly fresh")
	}
	server.liveStore = nil
}

func TestPhoenixUnwindRequiresNewSignatureForPartialFillOrExpiryAndReconcilesBeforeSending(t *testing.T) {
	for _, kind := range []string{"partial", "expired", "unknown", "valid", "already_sent", "not_reduce_only", "wrong_action"} {
		t.Run(kind, func(t *testing.T) {
			feed := newRecoveryTestFeed()
			server, accounts := recoveryPhoenixContext(t, feed)
			q := &domain.SigningRequest{ID: "armed", ClientOrderID: "42", Venue: "phoenix", Account: "phi-owner", Signer: "phi-agent", Action: "unwind", Amount: 1, ReduceOnly: true, ExpiresAt: time.Now().Add(30 * time.Second)}
			session := &LiveSession{ID: "session", accounts: accounts, ArmedUnwindReq: q, ArmedUnwindSigned: &domain.SignedAction{SignerAddress: "phi-agent"}, Leg1Fill: &normFill{Filled: true, FilledAmount: 1}}
			want := unwindConfirmed
			switch kind {
			case "partial":
				session.Leg1Fill.FilledAmount = .5
				want = unwindNeedsSignature
			case "expired":
				q.ExpiresAt = time.Now().Add(-time.Second)
				want = unwindNeedsSignature
			case "unknown":
				feed.lookupErr = orders.ErrUncertain
				want = unwindUnconfirmed
			case "already_sent":
				q.ExpiresAt = time.Now().Add(-time.Second)
				feed.exact[q.ID] = &normFill{Filled: true, FilledAmount: 1, OrderID: "old-signature", Status: "filled"}
			case "not_reduce_only":
				q.ReduceOnly = false
				want = unwindNeedsSignature
			case "wrong_action":
				q.Action = "open"
				want = unwindNeedsSignature
			}
			result := server.fireUnwind(context.Background(), session)
			if result.Status != want {
				t.Fatal(kind, result)
			}
			wantSends := int32(0)
			if kind == "valid" {
				wantSends = 1
			}
			if feed.sends.Load() != wantSends {
				t.Fatal("expired/partial/pending recovery was broadcast", feed.sends.Load())
			}
			if kind == "valid" {
				session.ArmedUnwindReq.ExpiresAt = time.Now().Add(-time.Second)
				if again := server.fireUnwind(context.Background(), session); !again.Confirmed() || feed.sends.Load() != 1 {
					t.Fatal("recovery replay sent again", again)
				}
			}
		})
	}
}

func TestPendingPhoenixCounterpartDoesNotTriggerAnotherVenueUnwind(t *testing.T) {
	feed := newRecoveryTestFeed()
	feed.pending = true
	server, accounts := recoveryPhoenixContext(t, feed)
	session := &LiveSession{accounts: accounts, ArmedUnwindReq: &domain.SigningRequest{Venue: "hyperliquid", Account: "0xwallet", Amount: 1, ReduceOnly: true}, ArmedUnwindSigned: &domain.SignedAction{}, Leg1Fill: &normFill{Filled: true, FilledAmount: 1}}
	if result := server.fireUnwind(context.Background(), session); result.Status != unwindUnconfirmed {
		t.Fatal("unresolved counterpart caused automatic hedge removal", result)
	}
}

func TestPhoenixRecoveryLookupKeepsUnknownExplicitAndRestoresExactPartialFill(t *testing.T) {
	feed := newRecoveryTestFeed()
	server, accounts := recoveryPhoenixContext(t, feed)
	session := &LiveSession{ID: "session", Plan: &domain.ExecutionPlan{ID: "plan", Asset: "SOL"}, Leg1: legPlan{venue: "phoenix", symbol: "SOL"}, Leg2: legPlan{venue: "hyperliquid", symbol: "SOL"}, accounts: accounts, CreatedAt: time.Now(), UpdatedAt: time.Now(), State: sessLeg1Submitted,
		Bindings: liveVenueBindings{Accounts: map[string]string{"phoenix": "phi-owner", "hyperliquid": "0xwallet"}}, Leg1OpenReq: &domain.SigningRequest{ID: "open", Venue: "phoenix", Account: "phi-owner", ClientOrderID: "41"}}
	feed.lookupErr = orders.ErrUncertain
	evidence := server.reconcileRecoveryOrders(context.Background(), session, sessLeg1Submitted)
	if !evidence.phoenixUnknown || evidence.leg1Known || session.Leg1Fill != nil {
		t.Fatal("unknown order became a position-derived zero", evidence)
	}
	feed.lookupErr = nil
	feed.exact["open"] = &normFill{Filled: true, FilledAmount: .75, OrderID: "confirmed-native-signature", Status: "partial_fill"}
	raw, err := marshalLiveSession(session)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := unmarshalLiveSession(raw)
	if err != nil {
		t.Fatal(err)
	}
	restored.accounts = accounts
	evidence = server.reconcileRecoveryOrders(context.Background(), restored, sessLeg1Submitted)
	if !evidence.leg1Known || evidence.phoenixUnknown || restored.Leg1Fill.FilledAmount != .75 || restored.Leg1Fill.OrderID != "confirmed-native-signature" {
		t.Fatal("restart lost exact order identity", evidence, restored.Leg1Fill)
	}
}

func phoenixRecoveryPosition(t *testing.T, server *Server) *executor.LivePosition {
	t.Helper()
	ctx := context.Background()
	result := &executor.ExecutionResult{PlanID: "phoenix-position", Asset: "SOL", State: executor.ExecStateClosing, StartedAt: time.Now().Add(-time.Minute), CompletedAt: time.Now(), Leg1: executor.LegResult{Venue: "phoenix", Symbol: "SOL-PERP", Side: "long", Submitted: true, Filled: true, Accepted: true, FilledAmount: 1, RequestedAmt: 1, AvgFillPrice: 100}}
	if err := server.liveStore.PersistFullResultAtomicForBindings(ctx, result, "phoenix", "hyperliquid", map[string]string{"phoenix": "phi-owner", "hyperliquid": "0xwallet"}, 100, 2); err != nil {
		t.Fatal(err)
	}
	position, err := server.liveStore.GetPosition(ctx, result.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	return position
}

func TestPhoenixClosePendingRemainsPendingAndRecoversExactPartialAfterRecreation(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	feed := newRecoveryTestFeed()
	_, accounts := recoveryPhoenixContext(t, feed)
	server.live.accounts = accounts.leases["phoenix"].registry
	position := phoenixRecoveryPosition(t, server)
	q := &domain.SigningRequest{PositionID: position.ID, Leg: 1, Venue: "phoenix", Account: "phi-owner", ClientOrderID: "42", Amount: 1, Action: "emergency_close", ReduceOnly: true, CreatedAt: time.Now().Add(-time.Second)}
	if err := server.liveStore.UpsertCloseOutcome(context.Background(), executor.CloseOutcome{PositionID: position.ID, Leg: 1, Venue: "phoenix", ClientOrderID: "42", RequestedAmount: 1, Resolved: false}); err != nil {
		t.Fatal(err)
	}
	feed.lookupErr = orders.ErrUncertain
	server.awaitCloseFill(q, "")
	progress, err := server.liveStore.GetCloseProgress(context.Background(), position.ID)
	if err != nil || progress.Pending != 1 {
		t.Fatal("timeout allowed duplicate close", progress, err)
	}
	feed.lookupErr = nil
	feed.closeRequest = q
	feed.closeFill = &normFill{Filled: true, FilledAmount: .4, AvgFillPrice: 100, OrderID: "durable-close", Status: "partial_fill"}
	server.reconcilePhoenixCloseOutcomes(context.Background(), position)
	progress, err = server.liveStore.GetCloseProgress(context.Background(), position.ID)
	if err != nil || progress.Pending != 0 || progress.Failed != 1 {
		t.Fatal("partial close did not resolve for bounded fresh retry", progress, err)
	}
	// An old waiter cannot regress the exact terminal outcome to pending.
	if err := server.liveStore.UpsertCloseOutcome(context.Background(), executor.CloseOutcome{PositionID: position.ID, Leg: 1, Venue: "phoenix", ClientOrderID: "42", RequestedAmount: 1, Resolved: false}); err != nil {
		t.Fatal(err)
	}
	progress, _ = server.liveStore.GetCloseProgress(context.Background(), position.ID)
	if progress.Pending != 0 {
		t.Fatal("terminal close regressed to pending")
	}
}

func TestPhoenixFlatProofIsBlockedUntilOutstandingOrdersResolve(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	feed := newRecoveryTestFeed()
	_, accounts := recoveryPhoenixContext(t, feed)
	server.live.accounts = accounts.leases["phoenix"].registry
	position := phoenixRecoveryPosition(t, server)
	feed.pending = true
	state, err := server.inspectPositionVenueTruth(context.Background(), position, time.Now().Add(-time.Second))
	if err != nil || state != positionExposureUnknown {
		t.Fatal("pending native transaction was marked flat", state, err)
	}
	feed.pending = false
	state, err = server.inspectPositionVenueTruth(context.Background(), position, time.Now().Add(-time.Second))
	if err != nil || state != positionExposureFlat {
		t.Fatal("resolved flat account did not become provable", state, err)
	}
}

func TestPhoenixCloseReconcilesExternalFlatWithoutAnotherOrder(t *testing.T) {
	for _, kind := range []string{"degraded", "closing", "missing fill", "unresolved", "refresh unavailable", "stale"} {
		t.Run(kind, func(t *testing.T) {
			server, db := newResidualExposureServer(t)
			feed := newRecoveryTestFeed()
			_, accounts := recoveryPhoenixContext(t, feed)
			server.live.accounts = accounts.leases["phoenix"].registry
			position := phoenixRecoveryPosition(t, server)
			state := "degraded"
			if kind == "closing" {
				state = "closing"
			}
			if _, err := db.Exec(`UPDATE live_positions SET state=?, updated_at=? WHERE id=?`, state, time.Now().Add(-time.Minute).Format(time.RFC3339), position.ID); err != nil {
				t.Fatal(err)
			}
			if kind == "missing fill" {
				if _, err := db.Exec(`DELETE FROM live_fills WHERE position_id=?`, position.ID); err != nil {
					t.Fatal(err)
				}
				session := &LiveSession{ID: "unconfirmed-open", State: sessDegraded,
					Plan:     &domain.ExecutionPlan{ID: position.PlanID, Asset: position.Asset},
					Bindings: liveVenueBindings{Accounts: position.AccountBindings},
					Leg1:     legPlan{venue: "phoenix", symbol: "SOL-PERP"}, Leg2: legPlan{venue: "hyperliquid", symbol: "SOL"},
					CreatedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now().Add(-time.Minute),
				}
				payload, err := marshalLiveSession(session)
				if err != nil {
					t.Fatal(err)
				}
				if err := server.liveStore.UpsertDurableSession(context.Background(), executor.DurableSessionRecord{
					ID: session.ID, State: string(session.State), Payload: payload, Asset: position.Asset,
					AccountBindings: position.AccountBindings, HasExposure: true, CreatedAt: session.CreatedAt, ExpiresAt: time.Now().Add(time.Minute),
				}); err != nil {
					t.Fatal(err)
				}
			}
			for venue, agent := range map[string]string{"phoenix": "phi-agent", "hyperliquid": "0xagent"} {
				if err := server.liveStore.UpsertAgentAuthorization(context.Background(), venue, position.AccountBindings[venue], agent); err != nil {
					t.Fatal(err)
				}
			}
			// The opening attempt may still be submitted locally after an external
			// close. It must resolve before a fresh flat snapshot can close the row.
			feed.pending = true
			switch kind {
			case "unresolved":
				feed.lookupErr = orders.ErrUncertain
			case "refresh unavailable":
				feed.refreshErr = errors.New("account unavailable")
			case "stale":
				stale := time.Now().Add(-time.Hour)
				feed.positionsAt = &stale
			}
			request := httptest.NewRequest("POST", "/api/v1/live/close/"+position.ID, jsonBody(t, map[string]any{
				"accounts": position.AccountBindings,
				"agents":   map[string]string{"phoenix": "phi-agent", "hyperliquid": "0xagent"},
			}))
			response := httptest.NewRecorder()
			server.handleLiveClose(response, request)
			wantClosed := kind == "degraded" || kind == "closing" || kind == "missing fill"
			var body struct {
				ReconciledClosed bool                    `json:"reconciled_closed"`
				SigningRequests  []domain.SigningRequest `json:"signing_requests"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			stored, err := server.liveStore.GetPosition(context.Background(), position.ID)
			if err != nil {
				t.Fatal(err)
			}
			if wantClosed {
				if response.Code != http.StatusOK || !body.ReconciledClosed || stored.State != "closed" {
					t.Fatalf("external close not reconciled: %d %s state=%s", response.Code, response.Body.String(), stored.State)
				}
			} else if response.Code != http.StatusConflict || body.ReconciledClosed || stored.State == "closed" {
				t.Fatalf("unknown exposure was marked closed: %d %s state=%s", response.Code, response.Body.String(), stored.State)
			}
			if len(body.SigningRequests) != 0 || feed.sends.Load() != 0 {
				t.Fatal("flat recovery created or sent another order")
			}
			fills, err := server.liveStore.GetFills(context.Background(), position.ID)
			if kind == "missing fill" {
				if err != nil || len(fills) != 0 {
					t.Fatal("flat snapshot fabricated historical execution", fills, err)
				}
			} else if err != nil || len(fills) != 1 || !fills[0].Filled || fills[0].FilledAmount != 1 {
				t.Fatal("historical fill was erased", fills, err)
			}
		})
	}
}

func TestMissingPhoenixNativeSymbolNeverFallsBackToNormalizedAssetForFlatProof(t *testing.T) {
	server, db := newResidualExposureServer(t)
	feed := newRecoveryTestFeed()
	_, accounts := recoveryPhoenixContext(t, feed)
	server.live.accounts = accounts.leases["phoenix"].registry
	position := phoenixRecoveryPosition(t, server)
	if _, err := db.Exec(`UPDATE live_fills SET symbol='' WHERE position_id=?`, position.ID); err != nil {
		t.Fatal(err)
	}
	if state, err := server.inspectPositionVenueTruth(context.Background(), position, time.Now().Add(-time.Second)); err == nil || state != positionExposureUnknown {
		t.Fatal("missing native symbol was treated as normalized-asset flat", state, err)
	}
}
