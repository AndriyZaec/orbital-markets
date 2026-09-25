package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	_ "modernc.org/sqlite"
)

func TestOpportunitySignalsRebuildLazilyAndReuseRows(t *testing.T) {
	database := newSignalTestDB(t)
	bucket := time.Now().UTC().Truncate(time.Hour).Unix()
	if _, err := database.Exec(`INSERT INTO market_snapshots_1h VALUES
		('pacifica', 'SOL', ?, 0),
		('hyperliquid', 'SOL', ?, 0.0002)`, bucket, bucket); err != nil {
		t.Fatal(err)
	}
	jobs := make(chan func(), 2)
	server := newSignalTestServer(database)
	server.signals.launch = func(job func()) { jobs <- job }
	opportunity := signalTestOpportunity()

	responses, err := server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{opportunity})
	if err != nil {
		t.Fatal(err)
	}
	if responses[0].Signal7d != nil {
		t.Fatal("cold request should not wait for signal calculation")
	}
	if responses[0].Signal7dState != "loading" {
		t.Fatalf("cold signal state = %q, want loading", responses[0].Signal7dState)
	}
	(<-jobs)()

	responses, err = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{opportunity})
	if err != nil {
		t.Fatal(err)
	}
	if responses[0].Signal7d == nil || responses[0].Signal7d.Samples != 1 {
		t.Fatalf("signal = %#v, want one sample", responses[0].Signal7d)
	}
	if responses[0].Signal7dState != "ready" || responses[0].Signal7dVersion == 0 {
		t.Fatalf("published signal metadata = %+v", responses[0])
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	changed := opportunity
	changed.AnnualizedGrossEdge = 0.21
	if _, err := server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{changed}); err != nil {
		t.Fatal(err)
	}
	(<-jobs)()
	server.signals.mu.Lock()
	gotVersion := server.signals.publication.opportunityVersion
	server.signals.mu.Unlock()
	if want := opportunitySignalVersion([]domain.Opportunity{changed}); gotVersion != want {
		t.Fatalf("opportunity version = %d, want %d", gotVersion, want)
	}
}

func TestOpportunitySignalsRunsFollowUpForInvalidationDuringBuild(t *testing.T) {
	database := newSignalTestDB(t)
	jobs := make(chan func(), 1)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	calls := 0
	server := newSignalTestServer(database)
	server.signals.launch = func(job func()) { jobs <- job }
	server.signals.build = func(
		_ context.Context,
		opportunities []domain.Opportunity,
		opportunityVersion uint64,
		source opportunitySignalSource,
		_ opportunitySignalPublication,
		_ bool,
	) (opportunitySignalBuild, error) {
		calls++
		if calls == 1 {
			close(firstStarted)
			<-releaseFirst
		} else if calls == 2 {
			close(secondStarted)
		}
		signals := make(map[string]scanner.OpportunitySignal, len(opportunities))
		for _, opportunity := range opportunities {
			signals[opportunity.ID] = scanner.OpportunitySignal{Samples: calls}
		}
		sourceChanged := source.fundingRows == nil
		source.fundingRows = []scanner.SignalFundingRow{}
		return opportunitySignalBuild{
			source:        source,
			sourceChanged: sourceChanged,
			publication: opportunitySignalPublication{
				opportunityVersion: opportunityVersion,
				signals:            signals,
			},
		}, nil
	}

	first := signalTestOpportunity()
	if _, err := server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{first}); err != nil {
		t.Fatal(err)
	}
	workerDone := make(chan struct{})
	go func() {
		(<-jobs)()
		close(workerDone)
	}()
	<-firstStarted
	changed := first
	changed.AnnualizedGrossEdge = 0.25
	if _, err := server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{changed}); err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)
	<-secondStarted
	<-workerDone

	server.signals.mu.Lock()
	defer server.signals.mu.Unlock()
	if server.signals.publishedVersion != server.signals.desiredVersion || server.signals.workerRunning {
		t.Fatalf("versions = published %d desired %d, running %v",
			server.signals.publishedVersion, server.signals.desiredVersion, server.signals.workerRunning)
	}
	if signal := server.signals.publication.signals[first.ID]; signal.Samples != 2 {
		t.Fatalf("published signal = %+v, want follow-up build", signal)
	}
}

func TestOpportunitySignalsAdvancesPublicationWhenInputsRevertDuringBuild(t *testing.T) {
	database := newSignalTestDB(t)
	jobs := make(chan func(), 1)
	buildStarted := make(chan struct{})
	releaseBuild := make(chan struct{})
	server := newSignalTestServer(database)
	server.signals.launch = func(job func()) { jobs <- job }

	original := signalTestOpportunity()
	originalVersion := opportunitySignalVersion([]domain.Opportunity{original})
	server.signals.source = opportunitySignalSource{fundingRows: []scanner.SignalFundingRow{}}
	server.signals.publication = opportunitySignalPublication{
		opportunityVersion: originalVersion,
		signals: map[string]scanner.OpportunitySignal{
			original.ID: {Samples: 24},
		},
	}
	server.signals.desiredVersion = 1
	server.signals.publishedVersion = 1
	server.signals.desiredOpportunityVersion = originalVersion
	server.signals.desiredOpportunities = []domain.Opportunity{original}
	server.signals.knownOpportunities = map[string]domain.Opportunity{original.ID: original}
	server.signals.nextSourceCheck = time.Now().Add(time.Hour)
	server.signals.build = func(
		_ context.Context,
		opportunities []domain.Opportunity,
		opportunityVersion uint64,
		source opportunitySignalSource,
		published opportunitySignalPublication,
		_ bool,
	) (opportunitySignalBuild, error) {
		if opportunityVersion != originalVersion {
			close(buildStarted)
			<-releaseBuild
		}
		if opportunityVersion == published.opportunityVersion {
			return opportunitySignalBuild{source: source, publication: published}, nil
		}
		return opportunitySignalBuild{
			source: source,
			publication: opportunitySignalPublication{
				opportunityVersion: opportunityVersion,
				signals: map[string]scanner.OpportunitySignal{
					opportunities[0].ID: {Samples: 12},
				},
			},
		}, nil
	}

	changed := original
	changed.AnnualizedGrossEdge = 0.25
	_, _ = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{changed})
	workerDone := make(chan struct{})
	go func() {
		(<-jobs)()
		close(workerDone)
	}()
	<-buildStarted
	_, _ = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{original})
	close(releaseBuild)
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after inputs reverted to the published state")
	}

	server.signals.mu.Lock()
	defer server.signals.mu.Unlock()
	if server.signals.publishedVersion != server.signals.desiredVersion || server.signals.workerRunning {
		t.Fatalf("versions = published %d desired %d, running %v",
			server.signals.publishedVersion, server.signals.desiredVersion, server.signals.workerRunning)
	}
}

func TestOpportunitySignalsPreservesLastSuccessfulSnapshotOnFailure(t *testing.T) {
	database := newSignalTestDB(t)
	jobs := make(chan func(), 2)
	fail := false
	server := newSignalTestServer(database)
	server.signals.launch = func(job func()) { jobs <- job }
	server.signals.build = func(
		_ context.Context,
		opportunities []domain.Opportunity,
		opportunityVersion uint64,
		source opportunitySignalSource,
		_ opportunitySignalPublication,
		_ bool,
	) (opportunitySignalBuild, error) {
		if fail {
			return opportunitySignalBuild{}, errors.New("database unavailable")
		}
		sourceChanged := source.fundingRows == nil
		source.fundingRows = []scanner.SignalFundingRow{}
		return opportunitySignalBuild{
			source:        source,
			sourceChanged: sourceChanged,
			publication: opportunitySignalPublication{
				opportunityVersion: opportunityVersion,
				signals: map[string]scanner.OpportunitySignal{
					opportunities[0].ID: {Samples: 24},
				},
			},
		}, nil
	}

	opportunity := signalTestOpportunity()
	_, _ = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{opportunity})
	(<-jobs)()
	server.signals.mu.Lock()
	publishedBefore := server.signals.publishedVersion
	server.signals.mu.Unlock()

	fail = true
	changed := opportunity
	changed.AnnualizedGrossEdge = 0.25
	_, _ = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{changed})
	(<-jobs)()
	server.signals.mu.Lock()
	publishedAfter := server.signals.publishedVersion
	signal := server.signals.publication.signals[opportunity.ID]
	server.signals.mu.Unlock()
	if publishedAfter != publishedBefore {
		t.Fatalf("failed build published version %d, want %d", publishedAfter, publishedBefore)
	}
	if signal.Samples != 24 {
		t.Fatalf("last successful signal was lost: %+v", signal)
	}
	responses, err := server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{changed})
	if err != nil {
		t.Fatal(err)
	}
	if responses[0].Signal7d == nil || responses[0].Signal7dState != "stale" {
		t.Fatalf("failed rebuild response = %+v, want stale successful signal", responses[0])
	}
}

func TestOpportunitySignalsRetriesFailureOnlyOncePerCycle(t *testing.T) {
	database := newSignalTestDB(t)
	jobs := make(chan func(), 2)
	retries := make(chan func(), 2)
	server := newSignalTestServer(database)
	server.signals.launch = func(job func()) { jobs <- job }
	server.signals.schedule = func(_ time.Duration, retry func()) { retries <- retry }
	builds := 0
	server.signals.build = func(
		context.Context,
		[]domain.Opportunity,
		uint64,
		opportunitySignalSource,
		opportunitySignalPublication,
		bool,
	) (opportunitySignalBuild, error) {
		builds++
		return opportunitySignalBuild{}, errors.New("database unavailable")
	}

	opportunity := signalTestOpportunity()
	_, _ = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{opportunity})
	(<-jobs)()
	(<-retries)()
	(<-jobs)()
	_, _ = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{opportunity})

	if builds != 2 {
		t.Fatalf("builds = %d, want initial attempt plus one retry", builds)
	}
	select {
	case <-jobs:
		t.Fatal("polling started another build after the retry was exhausted")
	default:
	}
	select {
	case <-retries:
		t.Fatal("failed retry scheduled another retry")
	default:
	}
}

func TestOpportunitySignalsRetainsTemporarilyAbsentPair(t *testing.T) {
	database := newSignalTestDB(t)
	jobs := make(chan func(), 2)
	server := newSignalTestServer(database)
	server.signals.launch = func(job func()) { jobs <- job }
	server.signals.build = func(
		_ context.Context,
		opportunities []domain.Opportunity,
		opportunityVersion uint64,
		source opportunitySignalSource,
		_ opportunitySignalPublication,
		_ bool,
	) (opportunitySignalBuild, error) {
		signals := make(map[string]scanner.OpportunitySignal, len(opportunities))
		for _, opportunity := range opportunities {
			signals[opportunity.ID] = scanner.OpportunitySignal{Samples: 24}
		}
		sourceChanged := source.fundingRows == nil
		source.fundingRows = []scanner.SignalFundingRow{}
		return opportunitySignalBuild{
			source:        source,
			sourceChanged: sourceChanged,
			publication: opportunitySignalPublication{
				opportunityVersion: opportunityVersion,
				signals:            signals,
			},
		}, nil
	}

	first := signalTestOpportunity()
	second := first
	second.ID = "BTC-pacifica-hyperliquid"
	second.Asset = "BTC"
	_, _ = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{first, second})
	(<-jobs)()
	second.AnnualizedGrossEdge = 0.30
	_, _ = server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{second})
	(<-jobs)()

	responses, err := server.opportunitiesWithSignals(context.Background(), []domain.Opportunity{first})
	if err != nil {
		t.Fatal(err)
	}
	if responses[0].Signal7d == nil || responses[0].Signal7d.Samples != 24 {
		t.Fatalf("reappearing pair signal = %+v", responses[0].Signal7d)
	}
}

func TestOpportunitySignalBuildDetectsUpdateWithinLatestBucket(t *testing.T) {
	database := newSignalTestDB(t)
	bucket := time.Now().UTC().Truncate(time.Hour).Unix()
	if _, err := database.Exec(`INSERT INTO market_snapshots_1h VALUES
		('pacifica', 'SOL', ?, 0),
		('hyperliquid', 'SOL', ?, 0.0002)`, bucket, bucket); err != nil {
		t.Fatal(err)
	}
	server := newSignalTestServer(database)
	opportunities := []domain.Opportunity{signalTestOpportunity()}
	version := opportunitySignalVersion(opportunities)
	first, err := server.buildOpportunitySignals(
		context.Background(), opportunities, version, opportunitySignalSource{}, opportunitySignalPublication{}, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE market_snapshots_1h SET funding_avg = 0.0004
		WHERE venue = 'hyperliquid' AND asset = 'SOL' AND bucket_unix = ?`, bucket); err != nil {
		t.Fatal(err)
	}
	second, err := server.buildOpportunitySignals(
		context.Background(), opportunities, version, first.source, first.publication, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.source.sourceBucket != first.source.sourceBucket || second.source.rollupRevision <= first.source.rollupRevision {
		t.Fatalf("sources = first %+v second %+v", first.source, second.source)
	}
	firstSignal := first.publication.signals[opportunities[0].ID]
	secondSignal := second.publication.signals[opportunities[0].ID]
	if secondSignal.AverageEdge <= firstSignal.AverageEdge {
		t.Fatalf("updated signal edge = %v, want greater than %v", secondSignal.AverageEdge, firstSignal.AverageEdge)
	}
}

func TestOpportunitySignalVersionTracksOpportunityInputs(t *testing.T) {
	a := []domain.Opportunity{{ID: "SOL-a-b-long-a"}}
	b := []domain.Opportunity{{ID: "SOL-a-b-long-b"}}
	if opportunitySignalVersion(a) == opportunitySignalVersion(b) {
		t.Fatal("different opportunity identities produced the same version")
	}
}

func TestOpportunitySignalSourceCheckPollsContentRevision(t *testing.T) {
	now := time.Date(2026, time.September, 23, 16, 0, 30, 0, time.UTC)
	if got, want := nextOpportunitySignalSourceCheck(now), now.Add(time.Minute); !got.Equal(want) {
		t.Fatalf("source check = %v, want %v", got, want)
	}
}

func newSignalTestDB(t *testing.T) *sql.DB {
	t.Helper()
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
			funding_avg REAL NOT NULL,
			PRIMARY KEY (venue, asset, bucket_unix)
		);
		CREATE TABLE rollup_revisions (name TEXT PRIMARY KEY, revision INTEGER NOT NULL);
		INSERT INTO rollup_revisions VALUES ('market_snapshots_1h', 0);
		CREATE TRIGGER market_snapshots_1h_revision_insert AFTER INSERT ON market_snapshots_1h BEGIN
			UPDATE rollup_revisions SET revision = revision + 1 WHERE name = 'market_snapshots_1h';
		END;
		CREATE TRIGGER market_snapshots_1h_revision_update AFTER UPDATE ON market_snapshots_1h
		WHEN OLD.venue IS NOT NEW.venue
			OR OLD.asset IS NOT NEW.asset
			OR OLD.bucket_unix IS NOT NEW.bucket_unix
			OR OLD.funding_avg IS NOT NEW.funding_avg
		BEGIN
			UPDATE rollup_revisions SET revision = revision + 1 WHERE name = 'market_snapshots_1h';
		END;
		CREATE TRIGGER market_snapshots_1h_revision_delete AFTER DELETE ON market_snapshots_1h BEGIN
			UPDATE rollup_revisions SET revision = revision + 1 WHERE name = 'market_snapshots_1h';
		END;
	`); err != nil {
		t.Fatal(err)
	}
	return database
}

func newSignalTestServer(database *sql.DB) *Server {
	return &Server{
		ctx:    context.Background(),
		db:     database,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func signalTestOpportunity() domain.Opportunity {
	return domain.Opportunity{
		ID:                  "SOL-pacifica-hyperliquid",
		Asset:               "SOL",
		VenuePair:           domain.VenuePair{VenueA: "pacifica", VenueB: "hyperliquid"},
		Direction:           domain.DirectionLongB,
		AnnualizedGrossEdge: 0.20,
	}
}
