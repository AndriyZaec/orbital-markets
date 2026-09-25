package pacifica

import (
	"context"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestFetchMarketDataReportsPriceComponentsBeforeFirstBookUpdate(t *testing.T) {
	receivedAt := time.Date(2026, time.September, 25, 13, 0, 1, 0, time.UTC)
	sourceTime := receivedAt.Add(-time.Second)
	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.now = func() time.Time { return receivedAt }
	generation := adapter.markStreamConnected()
	adapter.updatePrices(generation, []wsPrice{{
		Symbol: "SOL", Mark: "150", Oracle: "149", Mid: "149.5",
		Funding: "0.0001", OpenInterest: "1000", Timestamp: sourceTime.UnixMilli(),
	}})

	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	snapshot := snapshots[0]
	if snapshot.MarkPrice != 150 || snapshot.IndexPrice != 149 || snapshot.FundingRate != 0.0001 ||
		snapshot.OpenInterest != 1000 || snapshot.BidPrice != 149.5 || snapshot.AskPrice != 149.5 {
		t.Fatalf("legacy price values changed: %+v", snapshot)
	}
	if !snapshot.Timestamp.Equal(sourceTime) {
		t.Fatalf("legacy timestamp = %s, want %s", snapshot.Timestamp, sourceTime)
	}
	if snapshot.Health == nil {
		t.Fatal("market health is missing")
	}
	for name, component := range map[string]venue.ComponentHealth{
		"mark":          snapshot.Health.Mark,
		"index":         snapshot.Health.Index,
		"funding":       snapshot.Health.Funding,
		"open_interest": snapshot.Health.OpenInterest,
	} {
		if component.Status != venue.HealthAvailable || !component.SourceTime.Equal(sourceTime) ||
			!component.ReceivedAt.Equal(receivedAt) || component.SourceGeneration != generation {
			t.Fatalf("%s health = %+v", name, component)
		}
	}
	if snapshot.Health.Book.Status != venue.HealthUnavailable || snapshot.Health.Book.Reason != venue.HealthReasonNotObserved {
		t.Fatalf("mid fallback was reported as an observed book: %+v", snapshot.Health.Book)
	}
	if snapshot.Health.Stream.Status != venue.HealthAvailable || snapshot.Health.Stream.Generation != generation ||
		!snapshot.Health.Stream.ReceivedAt.Equal(receivedAt) {
		t.Fatalf("stream health = %+v", snapshot.Health.Stream)
	}
	if snapshot.Health.REST.Status != venue.HealthUnavailable || snapshot.Health.REST.Reason != venue.HealthReasonNotObserved {
		t.Fatalf("REST health = %+v", snapshot.Health.REST)
	}

	adapter.markStreamDisconnected(generation)
	snapshots, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots after disconnect = %+v, err = %v", snapshots, err)
	}
	snapshot = snapshots[0]
	if snapshot.MarkPrice != 150 || snapshot.BidPrice != 149.5 || !snapshot.Timestamp.Equal(sourceTime) {
		t.Fatalf("disconnect changed legacy snapshot: %+v", snapshot)
	}
	if snapshot.Health.Stream.Status != venue.HealthUnavailable ||
		snapshot.Health.Stream.Reason != venue.HealthReasonDisconnected {
		t.Fatalf("disconnected stream health = %+v", snapshot.Health.Stream)
	}
}

func TestFetchMarketDataReportsBBOAndInvalidPriceComponents(t *testing.T) {
	receivedAt := time.Date(2026, time.September, 25, 13, 0, 2, 0, time.UTC)
	priceTime := receivedAt.Add(-2 * time.Second)
	bookTime := receivedAt.Add(-time.Second)
	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.now = func() time.Time { return receivedAt }
	generation := adapter.markStreamConnected()
	adapter.updatePrices(generation, []wsPrice{{
		Symbol: "SOL", Mark: "NaN", Oracle: "149", Mid: "149.5",
		Funding: "0.0001", OpenInterest: "1000", Timestamp: priceTime.UnixMilli(),
	}})
	adapter.updateBBO(generation, wsBBO{
		Symbol: "SOL", BidPrice: "149", BidAmount: "2", AskPrice: "151", AskAmount: "3",
		Timestamp: bookTime.UnixMilli(),
	})

	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	snapshot := snapshots[0]
	if !math.IsNaN(snapshot.MarkPrice) || snapshot.BidPrice != 149 || snapshot.BidSize != 298 ||
		snapshot.AskPrice != 151 || snapshot.AskSize != 453 || !snapshot.Timestamp.Equal(bookTime) {
		t.Fatalf("legacy market values changed: %+v", snapshot)
	}
	if snapshot.Health.Mark.Status != venue.HealthUnavailable || snapshot.Health.Mark.Reason != venue.HealthReasonInvalidValue {
		t.Fatalf("invalid mark health = %+v", snapshot.Health.Mark)
	}
	if snapshot.Health.Index.Status != venue.HealthAvailable {
		t.Fatalf("valid index health = %+v", snapshot.Health.Index)
	}
	if snapshot.Health.Book.Status != venue.HealthAvailable || !snapshot.Health.Book.SourceTime.Equal(bookTime) ||
		!snapshot.Health.Book.ReceivedAt.Equal(receivedAt) || snapshot.Health.Book.SourceGeneration != generation {
		t.Fatalf("book health = %+v", snapshot.Health.Book)
	}
}
