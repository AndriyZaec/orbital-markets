package hyperliquid

import (
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestFetchMarketDataReportsRESTComponentsBeforeFirstBookUpdate(t *testing.T) {
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	fail := false
	invalidMark := false
	partial := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if partial {
			_, _ = io.WriteString(w, `[
				{"universe":[
					{"name":"SOL","maxLeverage":20,"szDecimals":2},
					{"name":"BTC","maxLeverage":40,"szDecimals":3}
				]},
				[{"markPx":"150","oraclePx":"149","funding":"0.0001","openInterest":"1000"}]
			]`)
			return
		}
		if invalidMark {
			_, _ = io.WriteString(w, `[
				{"universe":[{"name":"SOL","maxLeverage":20,"szDecimals":2}]},
				[{"markPx":"NaN","oraclePx":"149","funding":"0.0001","openInterest":"1000"}]
			]`)
			return
		}
		_, _ = io.WriteString(w, `[
			{"universe":[{"name":"SOL","maxLeverage":20,"szDecimals":2}]},
			[{"markPx":"150","oraclePx":"149","funding":"0.0001","openInterest":"1000"}]
		]`)
	}))
	defer server.Close()

	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.client = server.Client()
	adapter.marketURL = server.URL
	adapter.now = func() time.Time { return now }
	adapter.pollREST(context.Background())

	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v", snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.MarkPrice != 150 || snapshot.IndexPrice != 149 || snapshot.FundingRate != 0.0001 ||
		snapshot.OpenInterest != 1000 || snapshot.MaxLeverage != 20 {
		t.Fatalf("legacy market values changed: %+v", snapshot)
	}
	if !snapshot.Timestamp.Equal(now) {
		t.Fatalf("legacy timestamp = %s, want %s", snapshot.Timestamp, now)
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
		if component.Status != venue.HealthAvailable || !component.ReceivedAt.Equal(now) {
			t.Fatalf("%s health = %+v", name, component)
		}
	}
	if snapshot.Health.Book.Status != venue.HealthUnavailable || snapshot.Health.Book.Reason != venue.HealthReasonNotObserved {
		t.Fatalf("book health = %+v", snapshot.Health.Book)
	}
	if snapshot.Health.REST.Status != venue.HealthAvailable || snapshot.Health.REST.Generation != 1 {
		t.Fatalf("REST health = %+v", snapshot.Health.REST)
	}
	if snapshot.Health.Stream.Status != venue.HealthUnavailable || snapshot.Health.Stream.Reason != venue.HealthReasonNotObserved {
		t.Fatalf("stream health = %+v", snapshot.Health.Stream)
	}

	fail = true
	adapter.pollREST(context.Background())
	snapshots, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots after REST failure = %+v, err = %v", snapshots, err)
	}
	snapshot = snapshots[0]
	if snapshot.MarkPrice != 150 || snapshot.IndexPrice != 149 || snapshot.OpenInterest != 1000 {
		t.Fatalf("REST failure changed legacy values: %+v", snapshot)
	}
	if snapshot.Health.REST.Status != venue.HealthUnavailable || snapshot.Health.REST.Reason != venue.HealthReasonFetchFailed {
		t.Fatalf("failed REST health = %+v", snapshot.Health.REST)
	}
	if snapshot.Health.Mark.Status != venue.HealthAvailable {
		t.Fatalf("last successful mark observation was lost: %+v", snapshot.Health.Mark)
	}

	fail = false
	invalidMark = true
	adapter.pollREST(context.Background())
	snapshots, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots after invalid mark = %+v, err = %v", snapshots, err)
	}
	snapshot = snapshots[0]
	if !math.IsNaN(snapshot.MarkPrice) {
		t.Fatalf("legacy non-finite mark value = %v, want NaN", snapshot.MarkPrice)
	}
	if snapshot.Health.Mark.Status != venue.HealthUnavailable || snapshot.Health.Mark.Reason != venue.HealthReasonInvalidValue {
		t.Fatalf("invalid mark health = %+v", snapshot.Health.Mark)
	}
	if snapshot.Health.Index.Status != venue.HealthAvailable || snapshot.Health.REST.Status != venue.HealthAvailable {
		t.Fatalf("valid REST components were degraded: %+v", snapshot.Health)
	}

	invalidMark = false
	partial = true
	adapter.pollREST(context.Background())
	snapshots, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots after partial REST response = %+v, err = %v", snapshots, err)
	}
	snapshot = snapshots[0]
	if snapshot.MarkPrice != 150 || snapshot.Health.Mark.Status != venue.HealthAvailable {
		t.Fatalf("valid partial observation was not preserved: %+v", snapshot)
	}
	if snapshot.Health.REST.Status != venue.HealthUnavailable || snapshot.Health.REST.Reason != venue.HealthReasonIncompleteSnapshot {
		t.Fatalf("partial REST health = %+v", snapshot.Health.REST)
	}
}

func TestFetchMarketDataPreservesBookAfterStreamDisconnect(t *testing.T) {
	receivedAt := time.Date(2026, time.September, 25, 12, 0, 1, 0, time.UTC)
	sourceTime := receivedAt.Add(-time.Second)
	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.now = func() time.Time { return receivedAt }
	adapter.assets["SOL"] = &assetState{}

	generation := adapter.markStreamConnected()
	adapter.applyBBO(generation, bboData{
		Coin: "SOL",
		Time: sourceTime.UnixMilli(),
		BBO: []bboLevel{
			{Px: "149", Sz: "2"},
			{Px: "151", Sz: "3"},
		},
	})

	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	snapshot := snapshots[0]
	if snapshot.BidPrice != 149 || snapshot.BidSize != 298 || snapshot.AskPrice != 151 || snapshot.AskSize != 453 {
		t.Fatalf("legacy book values changed: %+v", snapshot)
	}
	if !snapshot.Timestamp.Equal(sourceTime) {
		t.Fatalf("legacy timestamp = %s, want %s", snapshot.Timestamp, sourceTime)
	}
	if snapshot.Health.Book.Status != venue.HealthAvailable ||
		!snapshot.Health.Book.SourceTime.Equal(sourceTime) ||
		!snapshot.Health.Book.ReceivedAt.Equal(receivedAt) ||
		snapshot.Health.Book.SourceGeneration != generation {
		t.Fatalf("book health = %+v", snapshot.Health.Book)
	}
	if snapshot.Health.Stream.Status != venue.HealthAvailable || snapshot.Health.Stream.Generation != generation ||
		!snapshot.Health.Stream.ReceivedAt.Equal(receivedAt) {
		t.Fatalf("stream health = %+v", snapshot.Health.Stream)
	}

	adapter.markStreamDisconnected(generation)
	snapshots, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots after disconnect = %+v, err = %v", snapshots, err)
	}
	snapshot = snapshots[0]
	if snapshot.BidPrice != 149 || !snapshot.Timestamp.Equal(sourceTime) {
		t.Fatalf("disconnect changed legacy snapshot: %+v", snapshot)
	}
	if snapshot.Health.Stream.Status != venue.HealthUnavailable ||
		snapshot.Health.Stream.Reason != venue.HealthReasonDisconnected {
		t.Fatalf("disconnected stream health = %+v", snapshot.Health.Stream)
	}
}

func TestFetchMarketDataReportsBookObservedWithoutSourceTime(t *testing.T) {
	receivedAt := time.Date(2026, time.September, 25, 12, 0, 1, 0, time.UTC)
	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.now = func() time.Time { return receivedAt }
	adapter.assets["SOL"] = &assetState{}

	generation := adapter.markStreamConnected()
	adapter.applyBBO(generation, bboData{
		Coin: "SOL",
		BBO: []bboLevel{
			{Px: "149", Sz: "2"},
			{Px: "151", Sz: "3"},
		},
	})

	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, err = %v", snapshots, err)
	}
	snapshot := snapshots[0]
	if !snapshot.Timestamp.Equal(receivedAt) {
		t.Fatalf("legacy timestamp = %s, want local time %s", snapshot.Timestamp, receivedAt)
	}
	if snapshot.Health.Book.Status != venue.HealthAvailable || !snapshot.Health.Book.SourceTime.IsZero() ||
		!snapshot.Health.Book.ReceivedAt.Equal(receivedAt) {
		t.Fatalf("book health = %+v", snapshot.Health.Book)
	}
}
