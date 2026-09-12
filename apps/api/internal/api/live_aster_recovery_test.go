package api

import (
	"context"
	"errors"
	"testing"
	"time"
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
