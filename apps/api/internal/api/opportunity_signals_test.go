package api

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	_ "modernc.org/sqlite"
)

func TestOpportunitySignalsRebuildLazily(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`
		CREATE TABLE market_snapshots_1h (
			venue TEXT NOT NULL,
			asset TEXT NOT NULL,
			bucket_unix INTEGER NOT NULL,
			funding_avg REAL NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	bucket := time.Now().UTC().Truncate(time.Hour).Unix()
	if _, err := database.Exec(`INSERT INTO market_snapshots_1h VALUES
		('pacifica', 'SOL', ?, 0),
		('hyperliquid', 'SOL', ?, 0.0002)`, bucket, bucket); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		ctx:    context.Background(),
		db:     database,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	opportunity := domain.Opportunity{
		ID:                  "SOL-pacifica-hyperliquid-long-b",
		Asset:               "SOL",
		VenuePair:           domain.VenuePair{VenueA: "pacifica", VenueB: "hyperliquid"},
		Direction:           domain.DirectionLongB,
		AnnualizedGrossEdge: 0.20,
	}

	responses, err := server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{opportunity})
	if err != nil {
		t.Fatal(err)
	}
	if responses[0].Signal7d != nil {
		t.Fatal("cold request should not wait for signal calculation")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.signalMu.Lock()
		ready := !server.signalRefreshRunning && server.signalSnapshot.signals[opportunity.ID].Samples == 1
		server.signalMu.Unlock()
		if ready {
			responses, err = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{opportunity})
			if err != nil {
				t.Fatal(err)
			}
			if responses[0].Signal7d == nil || responses[0].Signal7d.Samples != 1 {
				t.Fatalf("signal = %#v, want one sample", responses[0].Signal7d)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	server.signalMu.Lock()
	ready := !server.signalRefreshRunning && server.signalSnapshot.signals[opportunity.ID].Samples == 1
	server.signalMu.Unlock()
	if !ready {
		t.Fatal("background signal rebuild did not complete")
	}

	// Current opportunity inputs can be reclassified from cached hourly rows;
	// the seven-day database query must not run again for the same source bucket.
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	changed := opportunity
	changed.AnnualizedGrossEdge = 0.21
	_, err = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{changed})
	if err != nil {
		t.Fatal(err)
	}
	wantVersion := opportunitySignalVersion([]domain.Opportunity{changed})
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.signalMu.Lock()
		updated := !server.signalRefreshRunning && server.signalSnapshot.opportunityVersion == wantVersion
		server.signalMu.Unlock()
		if updated {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cached rows were not reused after opportunity inputs changed")
}

func TestOpportunitySignalVersionTracksOpportunityInputs(t *testing.T) {
	a := []domain.Opportunity{{ID: "SOL-a-b-long-a"}}
	b := []domain.Opportunity{{ID: "SOL-a-b-long-b"}}
	if opportunitySignalVersion(a) == opportunitySignalVersion(b) {
		t.Fatal("different opportunity identities produced the same version")
	}
}

func TestOpportunitySignalSourceCheckRetriesDelayedRollup(t *testing.T) {
	now := time.Date(2026, time.September, 23, 16, 0, 30, 0, time.UTC)
	delayedSource := now.Add(-2 * time.Hour).Truncate(time.Hour).Unix()
	if got, want := nextOpportunitySignalSourceCheck(now, delayedSource), now.Add(time.Minute); !got.Equal(want) {
		t.Fatalf("delayed source check = %v, want %v", got, want)
	}

	currentSource := now.Add(-time.Hour).Truncate(time.Hour).Unix()
	want := time.Date(2026, time.September, 23, 17, 1, 0, 0, time.UTC)
	if got := nextOpportunitySignalSourceCheck(now, currentSource); !got.Equal(want) {
		t.Fatalf("current source check = %v, want %v", got, want)
	}
}
