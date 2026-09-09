package account

import (
	"testing"
	"time"
)

func TestAccountStatePublishesOnlyCompleteCoherentSnapshot(t *testing.T) {
	state := NewAccountState("0xABCD")
	createdAt := time.Now().Add(-time.Second)
	respondedAt := createdAt.Add(100 * time.Millisecond)
	mode := SnapshotPart{Mode: &PositionMode{OneWay: true}}
	margin := SnapshotPart{Margin: &MarginSummary{CanTrade: true, Equity: 100, Available: 90}}
	positions := []Position{{Symbol: "BTCUSDT", Side: "long", Size: 1, Leverage: 5}}

	if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-1", createdAt, respondedAt, mode); err != nil {
		t.Fatal(err)
	}
	if snapshot := state.snapshotAt(respondedAt); snapshot.Connected || !snapshot.LastUpdated.IsZero() {
		t.Fatalf("partial snapshot was published: %+v", snapshot)
	}
	if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-1", createdAt, respondedAt.Add(time.Millisecond), margin); err != nil {
		t.Fatal(err)
	}
	if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-1", createdAt, respondedAt.Add(2*time.Millisecond), SnapshotPart{Positions: &positions}); err != nil {
		t.Fatal(err)
	}
	snapshot := state.snapshotAt(respondedAt.Add(time.Second))
	if !snapshot.Connected || !snapshot.OneWayMode || !snapshot.CanTrade || snapshot.Equity != 100 || len(snapshot.Positions) != 1 {
		t.Fatalf("complete snapshot = %+v", snapshot)
	}
	snapshot.Positions[0].Size = 99
	if state.snapshotAt(respondedAt.Add(time.Second)).Positions[0].Size != 1 {
		t.Fatal("snapshot exposed mutable position state")
	}
	if state.snapshotAt(respondedAt.Add(browserHeartbeatTTL + time.Second)).Connected {
		t.Fatal("stale browser snapshot remained connected")
	}
}

func TestAccountStateRejectsMixedGenerations(t *testing.T) {
	state := NewAccountState("0xabcd")
	first := time.Now().Add(-time.Second)
	second := first.Add(100 * time.Millisecond)
	if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-2", second, second.Add(time.Millisecond), SnapshotPart{
		Mode: &PositionMode{OneWay: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-1", first, second.Add(2*time.Millisecond), SnapshotPart{
		Margin: &MarginSummary{CanTrade: true, Equity: 100, Available: 90},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := state.snapshotAt(second.Add(time.Second))
	if snapshot.SnapshotID != "snapshot-2" || snapshot.CanTradeKnown || snapshot.Connected {
		t.Fatalf("older generation contaminated state: %+v", snapshot)
	}
}

func TestAccountStatePublishesUnavailableReasonUntilNewSnapshot(t *testing.T) {
	state := NewAccountState("0xabcd")
	if err := state.MarkUnavailable("0xabcd", "0xagent", "Aster account requires a deposit"); err != nil {
		t.Fatal(err)
	}
	if snapshot := state.Snapshot(); snapshot.Connected || snapshot.UnavailableReason != "Aster account requires a deposit" {
		t.Fatalf("unavailable snapshot = %+v", snapshot)
	}

	now := time.Now()
	if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-1", now, now.Add(time.Millisecond), SnapshotPart{
		Mode: &PositionMode{OneWay: true},
	}); err != nil {
		t.Fatal(err)
	}
	if snapshot := state.Snapshot(); snapshot.UnavailableReason != "" {
		t.Fatalf("new snapshot retained unavailable reason: %+v", snapshot)
	}
}

func TestAccountStateAppliesCoherentRefreshWithoutRecheckingMode(t *testing.T) {
	state := NewAccountState("0xabcd")
	createdAt := time.Now().Add(-time.Second)
	mode := SnapshotPart{Mode: &PositionMode{OneWay: true}}
	margin := SnapshotPart{Margin: &MarginSummary{CanTrade: true, Equity: 100, Available: 90}}
	positions := []Position{{Symbol: "BTCUSDT", Side: "long", Size: 1, Leverage: 5}}
	parts := []SnapshotPart{mode, margin, {Positions: &positions}}
	for i, part := range parts {
		if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-1", createdAt, createdAt.Add(time.Duration(i+1)*time.Millisecond), part); err != nil {
			t.Fatal(err)
		}
	}

	refreshAt := createdAt.Add(time.Second)
	newMargin := SnapshotPart{Margin: &MarginSummary{CanTrade: true, Equity: 120, Available: 110}}
	if err := state.ApplyRefreshPart("0xabcd", "0xagent", "refresh-1", refreshAt, refreshAt.Add(time.Millisecond), newMargin); err != nil {
		t.Fatal(err)
	}
	if snapshot := state.snapshotAt(refreshAt.Add(2 * time.Millisecond)); snapshot.Equity != 100 || len(snapshot.Positions) != 1 {
		t.Fatalf("partial refresh was published: %+v", snapshot)
	}
	newPositions := []Position{}
	if err := state.ApplyRefreshPart("0xabcd", "0xagent", "refresh-1", refreshAt, refreshAt.Add(2*time.Millisecond), SnapshotPart{Positions: &newPositions}); err != nil {
		t.Fatal(err)
	}
	snapshot := state.snapshotAt(refreshAt.Add(3 * time.Millisecond))
	if !snapshot.Connected || !snapshot.OneWayMode || snapshot.Equity != 120 || len(snapshot.Positions) != 0 {
		t.Fatalf("complete refresh = %+v", snapshot)
	}
}

func TestAccountStateIgnoresDelayedOlderRefreshParts(t *testing.T) {
	state := NewAccountState("0xabcd")
	createdAt := time.Now().Add(-time.Second)
	positions := []Position{{Symbol: "BTCUSDT", Side: "long", Size: 1, Leverage: 5}}
	parts := []SnapshotPart{
		{Mode: &PositionMode{OneWay: true}},
		{Margin: &MarginSummary{CanTrade: true, Equity: 100, Available: 90}},
		{Positions: &positions},
	}
	for i, part := range parts {
		if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-1", createdAt, createdAt.Add(time.Duration(i+1)*time.Millisecond), part); err != nil {
			t.Fatal(err)
		}
	}

	olderAt := createdAt.Add(time.Second)
	newerAt := olderAt.Add(time.Second)
	olderMargin := SnapshotPart{Margin: &MarginSummary{CanTrade: true, Equity: 110, Available: 100}}
	newerMargin := SnapshotPart{Margin: &MarginSummary{CanTrade: true, Equity: 120, Available: 110}}
	newerPositions := []Position{}
	if err := state.ApplyRefreshPart("0xabcd", "0xagent", "refresh-old", olderAt, newerAt, olderMargin); err != nil {
		t.Fatal(err)
	}
	if err := state.ApplyRefreshPart("0xabcd", "0xagent", "refresh-new", newerAt, newerAt.Add(time.Millisecond), newerMargin); err != nil {
		t.Fatal(err)
	}
	if err := state.ApplyRefreshPart("0xabcd", "0xagent", "refresh-old", olderAt, newerAt.Add(2*time.Millisecond), SnapshotPart{Positions: &positions}); err != nil {
		t.Fatal(err)
	}
	if err := state.ApplyRefreshPart("0xabcd", "0xagent", "refresh-new", newerAt, newerAt.Add(3*time.Millisecond), SnapshotPart{Positions: &newerPositions}); err != nil {
		t.Fatal(err)
	}

	snapshot := state.snapshotAt(newerAt.Add(4 * time.Millisecond))
	if snapshot.SnapshotID != "refresh-new" || snapshot.Equity != 120 || len(snapshot.Positions) != 0 {
		t.Fatalf("older refresh contaminated newer state: %+v", snapshot)
	}
}

func TestAccountStateIgnoresRefreshSupersededByFullSnapshot(t *testing.T) {
	state := NewAccountState("0xabcd")
	createdAt := time.Now().Add(-2 * time.Second)
	positions := []Position{{Symbol: "BTCUSDT", Side: "long", Size: 1, Leverage: 5}}
	initialParts := []SnapshotPart{
		{Mode: &PositionMode{OneWay: true}},
		{Margin: &MarginSummary{CanTrade: true, Equity: 100, Available: 90}},
		{Positions: &positions},
	}
	for i, part := range initialParts {
		if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-1", createdAt, createdAt.Add(time.Duration(i+1)*time.Millisecond), part); err != nil {
			t.Fatal(err)
		}
	}

	refreshAt := createdAt.Add(time.Second)
	if err := state.ApplyRefreshPart("0xabcd", "0xagent", "refresh-1", refreshAt, refreshAt.Add(time.Millisecond), SnapshotPart{
		Margin: &MarginSummary{CanTrade: true, Equity: 110, Available: 100},
	}); err != nil {
		t.Fatal(err)
	}
	fullAt := refreshAt.Add(time.Second)
	fullPositions := []Position{}
	fullParts := []SnapshotPart{
		{Mode: &PositionMode{OneWay: true}},
		{Margin: &MarginSummary{CanTrade: true, Equity: 130, Available: 120}},
		{Positions: &fullPositions},
	}
	for i, part := range fullParts {
		if err := state.ApplySnapshotPart("0xabcd", "0xagent", "snapshot-2", fullAt, fullAt.Add(time.Duration(i+1)*time.Millisecond), part); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.ApplyRefreshPart("0xabcd", "0xagent", "refresh-1", refreshAt, fullAt.Add(4*time.Millisecond), SnapshotPart{Positions: &positions}); err != nil {
		t.Fatal(err)
	}

	snapshot := state.snapshotAt(fullAt.Add(5 * time.Millisecond))
	if snapshot.SnapshotID != "snapshot-2" || snapshot.Equity != 130 || len(snapshot.Positions) != 0 {
		t.Fatalf("delayed refresh contaminated full snapshot: %+v", snapshot)
	}
}
