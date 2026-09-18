package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestRecoveryExplicitlyRefreshesAsterAccountState(t *testing.T) {
	updatedAt := time.Now().UTC()
	feed := &fakeAccountFeed{refreshSnapshot: &liveAccountSnapshot{
		Venue: "aster", Account: fundingTestAsterOwner,
		PositionsUpdatedAt: updatedAt,
	}}
	session, release := recoverySessionWithAsterFeed(t, feed, updatedAt.Add(-time.Second))
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	refreshRecoveryAccountState(ctx, session, false)
	if !(&Server{}).waitForRecoveryAccountState(ctx, session, false) {
		t.Fatal("backend Aster account refresh was not ready")
	}
	if refreshes := feed.refreshes.Load(); refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes)
	}
	if !venuePositionStateReadyAfter(session.accounts, "aster", session.UpdatedAt) {
		t.Fatal("recovery did not publish a post-session Aster position generation")
	}
}

func TestRecoveryAsterRefreshFailureDoesNotInferFlat(t *testing.T) {
	updatedAt := time.Now().UTC()
	feed := &fakeAccountFeed{
		snapshot: liveAccountSnapshot{
			Venue: "aster", Account: fundingTestAsterOwner,
			PositionsUpdatedAt: updatedAt.Add(-time.Minute),
		},
		refreshErr: errors.New("Aster unavailable"),
	}
	session, release := recoverySessionWithAsterFeed(t, feed, updatedAt.Add(-time.Second))
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	refreshRecoveryAccountState(ctx, session, false)
	if (&Server{}).waitForRecoveryAccountState(ctx, session, false) {
		t.Fatal("failed Aster refresh was treated as venue truth")
	}
	if venuePositionStateReadyAfter(session.accounts, "aster", session.UpdatedAt) {
		t.Fatal("previous Aster snapshot was relabeled as fresh flat truth")
	}
}

func TestRecoveryStartsBothVenueRefreshesConcurrently(t *testing.T) {
	aster := &fakeAccountFeed{refreshStarted: make(chan struct{}), refreshRelease: make(chan struct{})}
	pacifica := &fakeAccountFeed{refreshStarted: make(chan struct{}), refreshRelease: make(chan struct{})}
	defer close(aster.refreshRelease)
	defer close(pacifica.refreshRelease)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"aster":    &fixedAccountFeedFactory{feed: aster},
		"pacifica": &fixedAccountFeedFactory{feed: pacifica},
	}, accountFeedRegistryConfig{})
	asterLease, err := registry.AcquireRecovery("aster", fundingTestAsterOwner)
	if err != nil {
		t.Fatal(err)
	}
	defer asterLease.Release()
	pacificaLease, err := registry.AcquireRecovery("pacifica", "sol-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer pacificaLease.Release()
	session := &LiveSession{
		Leg1: legPlan{venue: "aster"}, Leg2: legPlan{venue: "pacifica"},
		accounts: &liveAccountContext{leases: map[string]*accountFeedLease{
			"aster": asterLease, "pacifica": pacificaLease,
		}},
	}

	refreshRecoveryAccountState(ctx, session, true)
	for venueName, started := range map[string]<-chan struct{}{
		"aster": aster.refreshStarted, "pacifica": pacifica.refreshStarted,
	} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("%s refresh did not start while the other venue was blocked", venueName)
		}
	}
}

func TestRecoveryReconcilesAsterOrderAfterRestart(t *testing.T) {
	feed := &fakeAccountFeed{waitFill: &normFill{
		OrderID: "42", Status: "partial_fill", FilledAmount: 0.75, AvgFillPrice: 100, Filled: true,
	}}
	session, release := recoverySessionWithAsterFeed(t, feed, time.Now())
	defer release()
	session.Leg1OpenReq = &domain.SigningRequest{
		Venue: "aster", Account: fundingTestAsterOwner, Symbol: "SOLUSDT",
		ClientOrderID: "orbital-leg1", Amount: 1,
	}

	evidence := (&Server{}).reconcileAsterRecoveryOrders(context.Background(), session, sessLeg1Submitted)
	if !evidence.leg1Known || evidence.detail != "" || session.Leg1Fill == nil ||
		session.Leg1Fill.OrderID != "42" || session.Leg1Fill.FilledAmount != 0.75 {
		t.Fatalf("recovery evidence = %+v, fill = %+v", evidence, session.Leg1Fill)
	}
}

func TestRecoveryKeepsAsterLookupFailureUncertain(t *testing.T) {
	feed := &fakeAccountFeed{waitErr: errors.New("read unavailable")}
	session, release := recoverySessionWithAsterFeed(t, feed, time.Now())
	defer release()
	session.Leg1OpenReq = &domain.SigningRequest{
		Venue: "aster", Account: fundingTestAsterOwner, Symbol: "SOLUSDT",
		ClientOrderID: "orbital-leg1", Amount: 1,
	}

	evidence := (&Server{}).reconcileAsterRecoveryOrders(context.Background(), session, sessLeg1Submitting)
	if evidence.leg1Known || evidence.detail == "" || session.Leg1Fill != nil {
		t.Fatalf("recovery evidence = %+v, fill = %+v", evidence, session.Leg1Fill)
	}
}

func TestRecoveryRequiresConfirmedFillWithoutPositionTruth(t *testing.T) {
	if hasConfirmedLeg1RecoveryFill(recoveryOrderEvidence{}, nil) {
		t.Fatal("missing venue evidence allowed automatic recovery")
	}
	if hasConfirmedLeg1RecoveryFill(recoveryOrderEvidence{leg1Known: true}, &normFill{}) {
		t.Fatal("confirmed no-fill allowed automatic recovery")
	}
	if !hasConfirmedLeg1RecoveryFill(recoveryOrderEvidence{leg1Known: true}, &normFill{
		Filled: true, FilledAmount: 0.4,
	}) {
		t.Fatal("confirmed partial fill did not allow bounded recovery")
	}
}

func TestRecoveryWithoutEvidencePersistsDurableDegradedState(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	server.live.sessions = NewSessionManager()
	now := time.Now()
	session := &LiveSession{
		ID: "session-no-evidence",
		Plan: &domain.ExecutionPlan{
			ID: "position-no-evidence", OpportunityID: "opportunity-1", Asset: "SOL",
			Notional: 10, Leverage: domain.ComputeLeverage(10, 2),
			Leg1: domain.Leg{Venue: "pacifica"}, Leg2: domain.Leg{Venue: "hyperliquid"},
		},
		Leg1: legPlan{venue: "pacifica", symbol: "SOL", side: domain.SideLong},
		Leg2: legPlan{venue: "hyperliquid", symbol: "SOL", side: domain.SideShort},
		Bindings: liveVenueBindings{Accounts: map[string]string{
			"pacifica": "sol-wallet", "hyperliquid": "0xwallet",
		}},
		State: sessLeg1Submitted, CreatedAt: now, UpdatedAt: now,
	}
	payload, err := marshalLiveSession(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.liveStore.UpsertDurableSession(context.Background(), executor.DurableSessionRecord{
		ID: session.ID, State: string(session.State), Payload: payload,
		AccountBindings: session.Bindings.Accounts, Asset: "SOL", HasExposure: true,
		ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	server.degradeRecoveryWithoutEvidence(session, "recovery timed out")

	record, err := server.liveStore.GetDurableSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != sessDegraded || record.State != string(sessDegraded) || !record.Terminal {
		t.Fatalf("session state = %q, durable record = %+v", session.State, record)
	}
}

func TestRecoveryMergesAsterRetryOrderOnlyOnce(t *testing.T) {
	feed := &fakeAccountFeed{waitFill: &normFill{
		OrderID: "retry-order", Status: "filled", FilledAmount: 0.4, AvgFillPrice: 101, Filled: true,
	}}
	session, release := recoverySessionWithAsterFeed(t, feed, time.Now())
	defer release()
	session.Leg2 = legPlan{venue: "aster", symbol: "SOLUSDT"}
	session.Leg1Fill = &normFill{FilledAmount: 1, AvgFillPrice: 100, Filled: true}
	session.Leg2Fill = &normFill{OrderID: "open-order", FilledAmount: 0.6, AvgFillPrice: 100, Filled: true}
	session.Leg2Attempts = 2
	session.Leg2RetryReq = &domain.SigningRequest{
		Venue: "aster", Account: fundingTestAsterOwner, Symbol: "SOLUSDT",
		ClientOrderID: "orbital-leg2-retry", Amount: 0.4,
	}

	server := &Server{}
	evidence := server.reconcileAsterRecoveryOrders(context.Background(), session, sessLeg2Submitted)
	if !evidence.leg2Known || session.Leg2Fill.FilledAmount != 1 || session.Leg2Fill.OrderID != "retry-order" {
		t.Fatalf("first retry recovery = %+v, fill = %+v", evidence, session.Leg2Fill)
	}
	server.reconcileAsterRecoveryOrders(context.Background(), session, sessLeg2Submitted)
	if session.Leg2Fill.FilledAmount != 1 {
		t.Fatalf("repeated retry recovery duplicated fill: %+v", session.Leg2Fill)
	}
}

func recoverySessionWithAsterFeed(
	t *testing.T,
	feed *fakeAccountFeed,
	updatedAt time.Time,
) (*LiveSession, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	factory := &fixedAccountFeedFactory{feed: feed}
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"aster": factory,
	}, accountFeedRegistryConfig{})
	lease, err := registry.AcquireRecovery("aster", fundingTestAsterOwner)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	session := &LiveSession{
		Leg1:      legPlan{venue: "aster", symbol: "SOLUSDT"},
		UpdatedAt: updatedAt,
		accounts: &liveAccountContext{leases: map[string]*accountFeedLease{
			"aster": lease,
		}},
	}
	return session, func() {
		lease.Release()
		cancel()
	}
}

type fixedAccountFeedFactory struct {
	feed liveAccountFeed
}

func (f *fixedAccountFeedFactory) Normalize(account string) (string, error) {
	return account, nil
}

func (f *fixedAccountFeedFactory) Start(context.Context, string) (liveAccountFeed, error) {
	return f.feed, nil
}
