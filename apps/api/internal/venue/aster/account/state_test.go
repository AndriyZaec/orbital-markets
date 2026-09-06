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
