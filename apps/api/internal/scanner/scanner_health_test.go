package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type mutableScannerAdapter struct {
	name    string
	data    []venue.MarketData
	err     error
	fetches int
}

func (a *mutableScannerAdapter) Name() string { return a.name }

func (a *mutableScannerAdapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	a.fetches++
	return a.data, a.err
}

func TestScanPublishesStableOpportunityHealth(t *testing.T) {
	now := time.Now()
	left := &mutableScannerAdapter{name: "aster", data: []venue.MarketData{healthyMarket("aster", "PIPPIN", 0.001, now)}}
	right := &mutableScannerAdapter{name: "pacifica", data: []venue.MarketData{healthyMarket("pacifica", "PIPPIN", -0.001, now)}}
	scanner := New(slog.New(slog.NewTextHandler(io.Discard, nil)), left, right)

	scanner.scan(context.Background())
	available := requireSingleOpportunity(t, scanner)
	if available.Status != domain.OpportunityAvailable || available.Generation != 1 {
		t.Fatalf("initial health = %q generation %d, want available generation 1", available.Status, available.Generation)
	}
	if available.SourceRevisions["aster"] != 1 || available.SourceRevisions["pacifica"] != 1 {
		t.Fatalf("initial source revisions = %+v, want both 1", available.SourceRevisions)
	}
	legacyID := available.ID + "-" + string(available.Direction)
	if resolved := scanner.FindOpportunity(legacyID); resolved == nil || resolved.ID != available.ID {
		t.Fatalf("legacy ID %q resolved to %+v, want %q", legacyID, resolved, available.ID)
	}

	left.data = nil
	scanner.scan(context.Background())
	degraded := requireSingleOpportunity(t, scanner)
	if degraded.ID != available.ID || degraded.Status != domain.OpportunityDegraded || degraded.ExecutionStatus != "blocked" {
		t.Fatalf("degraded opportunity = %+v, want stable blocked row", degraded)
	}
	if len(degraded.AvailabilityReasons) != 1 || degraded.AvailabilityReasons[0].Code != domain.OpportunityReasonMarketDataUnavailable || degraded.AvailabilityReasons[0].Venue != "aster" {
		t.Fatalf("degraded reasons = %+v", degraded.AvailabilityReasons)
	}
	if degraded.SourceRevisions["aster"] != 2 || degraded.SourceRevisions["pacifica"] != 2 {
		t.Fatalf("degraded source revisions = %+v, want both 2", degraded.SourceRevisions)
	}

	left.err = errors.New("temporary transport failure")
	scanner.scan(context.Background())
	unavailable := requireSingleOpportunity(t, scanner)
	if unavailable.Status != domain.OpportunityUnavailable || unavailable.Generation != 3 {
		t.Fatalf("continued outage health = %q generation %d, want unavailable generation 3", unavailable.Status, unavailable.Generation)
	}
	if len(unavailable.AvailabilityReasons) != 1 || unavailable.AvailabilityReasons[0].Code != domain.OpportunityReasonSourceFetchFailed {
		t.Fatalf("unavailable reasons = %+v", unavailable.AvailabilityReasons)
	}
	if unavailable.SourceRevisions["aster"] != 2 || unavailable.SourceRevisions["pacifica"] != 3 {
		t.Fatalf("failed fetch revisions = %+v, want aster preserved at 2 and pacifica 3", unavailable.SourceRevisions)
	}

	left.err = nil
	left.data = []venue.MarketData{healthyMarket("aster", "PIPPIN", -0.002, now)}
	scanner.scan(context.Background())
	recovered := requireSingleOpportunity(t, scanner)
	if recovered.ID != available.ID || recovered.Status != domain.OpportunityAvailable || recovered.Generation != 4 {
		t.Fatalf("recovered opportunity = %+v, want original identity available at generation 4", recovered)
	}
	if recovered.Direction == available.Direction {
		t.Fatalf("direction = %q, want reversal without identity change", recovered.Direction)
	}
	if !recovered.DetectedAt.Equal(available.DetectedAt) {
		t.Fatalf("detected_at changed from %s to %s", available.DetectedAt, recovered.DetectedAt)
	}
}

func TestBuildPlanRejectsDegradedOpportunityBeforeFetching(t *testing.T) {
	now := time.Now()
	left := &mutableScannerAdapter{name: "aster", data: []venue.MarketData{healthyMarket("aster", "PIPPIN", 0.001, now)}}
	right := &mutableScannerAdapter{name: "pacifica", data: []venue.MarketData{healthyMarket("pacifica", "PIPPIN", -0.001, now)}}
	scanner := New(slog.New(slog.NewTextHandler(io.Discard, nil)), left, right)
	scanner.scan(context.Background())
	opportunityID := requireSingleOpportunity(t, scanner).ID

	left.data = nil
	scanner.scan(context.Background())
	fetchesBeforePlan := left.fetches + right.fetches
	_, err := scanner.BuildPlan(context.Background(), opportunityID, 1, 100)
	var statusErr *OpportunityStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != domain.OpportunityDegraded {
		t.Fatalf("BuildPlan error = %#v, want degraded OpportunityStatusError", err)
	}
	if left.fetches+right.fetches != fetchesBeforePlan {
		t.Fatal("degraded opportunity triggered fresh market fetch")
	}
}

func TestBuildPlanRejectsFreshFundingDirectionChange(t *testing.T) {
	now := time.Now()
	left := &mutableScannerAdapter{name: "aster", data: []venue.MarketData{healthyMarket("aster", "PIPPIN", 0.001, now)}}
	right := &mutableScannerAdapter{name: "pacifica", data: []venue.MarketData{healthyMarket("pacifica", "PIPPIN", -0.001, now)}}
	scanner := New(slog.New(slog.NewTextHandler(io.Discard, nil)), left, right)
	scanner.scan(context.Background())
	opportunityID := requireSingleOpportunity(t, scanner).ID

	left.data[0].FundingRate = -0.002
	_, err := scanner.BuildPlan(context.Background(), opportunityID, 1, 100)
	if err == nil || !containsWarning([]string{err.Error()}, "funding direction changed") {
		t.Fatalf("BuildPlan error = %v, want funding direction change", err)
	}
}

func healthyMarket(venueName, asset string, fundingRate float64, now time.Time) venue.MarketData {
	return venue.MarketData{
		Venue: venueName, Asset: asset, MarketKey: asset, MarkPrice: 1, IndexPrice: 1,
		FundingRate: fundingRate, BidPrice: 0.999, BidSize: 10_000, AskPrice: 1.001,
		AskSize: 10_000, OpenInterest: 1_000_000, MaxLeverage: 10, Timestamp: now,
	}
}

func requireSingleOpportunity(t *testing.T, scanner *Scanner) domain.Opportunity {
	t.Helper()
	opportunities := scanner.Opportunities()
	if len(opportunities) != 1 {
		t.Fatalf("opportunities = %+v, want one", opportunities)
	}
	return opportunities[0]
}
