package account

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const owner = "11111111111111111111111111111111"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func snapshotMessage(t *testing.T) message {
	t.Helper()
	m, err := decodeREST(fixture(t, "trader-snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func deltaMessage(t *testing.T) message {
	t.Helper()
	var m message
	if err := json.Unmarshal(fixture(t, "trader-delta"), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func readyState(t *testing.T) (*State, uint64, time.Time) {
	t.Helper()
	state, err := NewState(owner)
	if err != nil {
		t.Fatal(err)
	}
	generation := state.begin()
	state.connected(generation)
	at := time.Now().UTC()
	if err := state.apply(generation, snapshotMessage(t), at, true); err != nil {
		t.Fatal(err)
	}
	return state, generation, at
}

func TestSnapshotDeltaKeepsExactCrossExposureAndSpot(t *testing.T) {
	s, generation, at := readyState(t)
	before := s.Snapshot()
	if !before.Fresh(at) || before.Sequence != 41 || before.Cross.Collateral != "9007199254740993" ||
		before.Cross.Positions["BTC"].BasePositionLots != "-9007199254740993" ||
		before.Cross.Orders["BTC"]["18446744073709551615"].SizeRemainingLots != "10" {
		t.Fatalf("incorrect snapshot: %+v", before)
	}
	if before.Cross.SpotCollaterals[0].Balance != "1000000001" || len(before.Cross.Splines["BTC"]) == 0 {
		t.Fatal("lost spot/spline inputs")
	}
	if err := s.apply(generation, deltaMessage(t), at.Add(time.Second), true); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot()
	if after.Sequence != 42 || after.Slot != 101 || after.Cross.Collateral != "9007199254740994" || len(after.Cross.Positions) != 1 || after.Cross.Positions["ETH"].BasePositionLots != "2500" ||
		len(after.Cross.Orders["BTC"]) != 0 || len(after.Cross.Splines) != 0 || len(after.Cross.Triggers) != 1 || after.Cross.SpotCollaterals[0].Balance != "1000000002" {
		t.Fatalf("incorrect delta result: %+v", after)
	}
	// No SOL perp position is needed to preserve SOL collateral.
	if _, exists := after.Cross.Positions["SOL"]; exists {
		t.Fatal("invented SOL position")
	}
	// Returned nested data must not mutate the account feed.
	after.Cross.Positions["ETH"] = Position{}
	after.Cross.SpotCollaterals[0].Balance = "0"
	after.Cross.Triggers["ETH"][0] = '['
	if current := s.Snapshot(); current.Cross.Positions["ETH"].BasePositionLots != "2500" || current.Cross.SpotCollaterals[0].Balance != "1000000002" || current.Cross.Triggers["ETH"][0] != '{' {
		t.Fatal("snapshot aliases internal state")
	}
}

func TestGapInvalidatesUntilFullSnapshotAndCannotPartiallyApply(t *testing.T) {
	s, generation, at := readyState(t)
	m := deltaMessage(t)
	bad := m.Deltas[0]
	sequence := uint64(44)
	bad.Sequence = &sequence
	m.Deltas = append(m.Deltas, bad)
	before := s.Snapshot().Cross
	if err := s.apply(generation, m, at.Add(time.Second), true); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("expected gap: %v", err)
	}
	broken := s.Snapshot()
	if broken.Synchronized || !broken.PositionsUpdatedAt.IsZero() || !reflect.DeepEqual(before, broken.Cross) || broken.Sequence != 41 {
		t.Fatal("gap exposed partial or authoritative stale state")
	}
	if err := s.apply(generation, deltaMessage(t), at.Add(2*time.Second), true); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("delta repaired a gap: %v", err)
	}
	if err := s.apply(generation, snapshotMessage(t), at.Add(3*time.Second), true); err != nil {
		t.Fatal(err)
	}
	if !s.Snapshot().Fresh(at.Add(3 * time.Second)) {
		t.Fatal("full snapshot did not recover")
	}
}

func TestDuplicatesIsolatedRowsAndOldReadsNeverRenewFreshness(t *testing.T) {
	s, generation, at := readyState(t)
	m := deltaMessage(t)
	*m.Deltas[0].Sequence = 41
	if err := s.apply(generation, m, at.Add(20*time.Second), true); err != nil {
		t.Fatal(err)
	}
	*m.Deltas[0].Index = 1
	if err := s.apply(generation, m, at.Add(25*time.Second), true); err != nil {
		t.Fatal(err)
	}
	old := snapshotMessage(t)
	*old.Slot = 99
	*old.Subaccounts[0].Sequence = 40
	if err := s.apply(generation, old, at.Add(29*time.Second), false); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); !got.ObservedAt.Equal(at) || got.Fresh(at.Add(Freshness+time.Nanosecond)) || got.Fresh(at.Add(-time.Nanosecond)) {
		t.Fatal("unrelated/stale traffic made cross state fresh")
	}
}

func TestReconnectFencesOldRESTAndRequiresNewStreamSnapshot(t *testing.T) {
	s, generation, at := readyState(t)
	s.disconnect(generation)
	if got := s.Snapshot(); got.Connected || got.Synchronized || !got.PositionsUpdatedAt.IsZero() {
		t.Fatal("disconnect left account ready")
	}
	next := s.begin()
	s.connected(next)
	if err := s.apply(generation, snapshotMessage(t), at.Add(time.Second), false); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Synchronized {
		t.Fatal("old generation restored readiness")
	}
	if err := s.apply(next, snapshotMessage(t), at.Add(2*time.Second), false); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Synchronized {
		t.Fatal("REST bootstrap authorized a stream without baseline")
	}
	if err := s.apply(next, deltaMessage(t), at.Add(3*time.Second), true); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("delta before stream snapshot: %v", err)
	}
	if err := s.apply(next, snapshotMessage(t), at.Add(4*time.Second), true); err != nil {
		t.Fatal(err)
	}
	if !s.Snapshot().Fresh(at.Add(4 * time.Second)) {
		t.Fatal("new stream snapshot not ready")
	}
}

func TestPeriodicSnapshotReplacesRowsAndRefreshesUnchangedAccount(t *testing.T) {
	s, generation, at := readyState(t)
	m := snapshotMessage(t)
	m.Subaccounts[0].Positions = nil
	m.Subaccounts[0].Orders = nil
	m.Subaccounts[0].Splines = nil
	m.Subaccounts[0].Triggers = nil
	m.Subaccounts[0].SpotCollaterals = nil
	if err := s.apply(generation, m, at.Add(15*time.Second), false); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot()
	if !after.Fresh(at.Add(40*time.Second)) || len(after.Cross.Positions)+len(after.Cross.Orders)+len(after.Cross.Splines)+len(after.Cross.Triggers)+len(after.Cross.SpotCollaterals) != 0 {
		t.Fatal("REST snapshot failed to replace or refresh idle state")
	}
}

func TestStreamSnapshotConfirmsRESTStateWithLowerSlot(t *testing.T) {
	for name, differentContent := range map[string]bool{"same content": false, "different content": true} {
		t.Run(name, func(t *testing.T) {
			s, err := NewState(owner)
			if err != nil {
				t.Fatal(err)
			}
			generation := s.begin()
			s.connected(generation)
			at := time.Now().UTC()
			if err := s.apply(generation, snapshotMessage(t), at, false); err != nil {
				t.Fatal(err)
			}
			before := s.Snapshot()
			if before.Synchronized {
				t.Fatal("REST bootstrap authorized stream without baseline")
			}
			m := snapshotMessage(t)
			*m.Slot = before.Slot - 2
			if differentContent {
				m.Subaccounts[0].Collateral = "1"
				m.Subaccounts[0].Positions = nil
			}
			observedAt := at.Add(time.Second)
			if err := s.apply(generation, m, observedAt, true); err != nil {
				t.Fatalf("stream baseline rejected: %v", err)
			}
			after := s.Snapshot()
			if !after.Fresh(observedAt) || after.UnavailableReason != "" || !after.ObservedAt.Equal(observedAt) || !after.PositionsUpdatedAt.Equal(observedAt) {
				t.Fatalf("stream baseline did not synchronize account: %+v", after)
			}
			if after.Sequence != before.Sequence || after.Slot != before.Slot || !reflect.DeepEqual(after.Cross, before.Cross) {
				t.Fatal("same-sequence lower-slot stream snapshot replaced REST state")
			}
		})
	}
}

func TestSnapshotWithOlderSequenceCannotReplaceCurrentState(t *testing.T) {
	for name, stream := range map[string]bool{"REST": false, "stream": true} {
		t.Run(name, func(t *testing.T) {
			s, generation, at := readyState(t)
			before := s.Snapshot()
			m := snapshotMessage(t)
			*m.Subaccounts[0].Sequence = before.Sequence - 1
			*m.Slot = before.Slot + 1
			err := s.apply(generation, m, at.Add(time.Second), stream)
			if stream {
				if err == nil || !strings.Contains(err.Error(), "behind") {
					t.Fatalf("expected behind error: %v", err)
				}
			} else if err != nil {
				t.Fatalf("stale REST snapshot was not silently ignored: %v", err)
			}
			after := s.Snapshot()
			if after.Sequence != before.Sequence || after.Slot != before.Slot || !after.ObservedAt.Equal(before.ObservedAt) || !reflect.DeepEqual(after.Cross, before.Cross) {
				t.Fatal("older sequence replaced or refreshed current state")
			}
			if stream && (after.Synchronized || !after.PositionsUpdatedAt.IsZero()) {
				t.Fatal("older stream snapshot left account synchronized")
			}
		})
	}
}

func TestDeltaAfterRESTRefreshUsesSequenceDespiteLowerSlot(t *testing.T) {
	for name, gap := range map[string]bool{"next sequence": false, "sequence gap": true} {
		t.Run(name, func(t *testing.T) {
			s, generation, at := readyState(t)
			rest := snapshotMessage(t)
			*rest.Slot += 3
			if err := s.apply(generation, rest, at.Add(15*time.Second), false); err != nil {
				t.Fatal(err)
			}
			before := s.Snapshot()
			m := deltaMessage(t)
			*m.Slot = before.Slot - 1
			if gap {
				*m.Deltas[0].Sequence = before.Sequence + 2
			}
			observedAt := at.Add(16 * time.Second)
			err := s.apply(generation, m, observedAt, true)
			after := s.Snapshot()
			if gap {
				if !errors.Is(err, ErrSequenceGap) {
					t.Fatalf("expected sequence gap: %v", err)
				}
				if after.Synchronized || !after.PositionsUpdatedAt.IsZero() || after.Sequence != before.Sequence || after.Slot != before.Slot || !reflect.DeepEqual(after.Cross, before.Cross) {
					t.Fatal("sequence gap applied or left authoritative state")
				}
				return
			}
			if err != nil {
				t.Fatalf("next-sequence lower-slot delta rejected: %v", err)
			}
			if !after.Fresh(observedAt) || !after.ObservedAt.Equal(observedAt) || after.Slot != before.Slot || after.Sequence != before.Sequence+1 || after.Cross.Collateral != "9007199254740994" || after.Cross.Positions["ETH"].BasePositionLots != "2500" {
				t.Fatalf("delta did not update account while retaining REST slot: %+v", after)
			}
		})
	}
}

func TestNewerSequenceSnapshotRetainsHigherSlot(t *testing.T) {
	s, generation, at := readyState(t)
	before := s.Snapshot()
	m := snapshotMessage(t)
	*m.Slot = before.Slot - 1
	*m.Subaccounts[0].Sequence = before.Sequence + 1
	m.Subaccounts[0].Collateral = "1"
	if err := s.apply(generation, m, at.Add(time.Second), true); err != nil {
		t.Fatal(err)
	}
	if after := s.Snapshot(); after.Slot != before.Slot || after.Sequence != before.Sequence+1 || after.Cross.Collateral != "1" || !after.Fresh(at.Add(time.Second)) {
		t.Fatalf("newer snapshot rejected or reduced slot: %+v", after)
	}
}

func TestInvalidAccountPayloadsFailClosed(t *testing.T) {
	for name, mutate := range map[string]func(*message){
		"owner":            func(m *message) { m.Authority = "other" },
		"PDA":              func(m *message) { *m.TraderPDAIndex = 1 },
		"missing PDA":      func(m *message) { m.TraderPDAIndex = nil },
		"missing slot":     func(m *message) { m.Slot = nil },
		"version":          func(m *message) { *m.Version = 2 },
		"missing cross":    func(m *message) { m.Subaccounts = m.Subaccounts[1:] },
		"duplicate cross":  func(m *message) { m.Subaccounts = append(m.Subaccounts, m.Subaccounts[0]) },
		"missing sequence": func(m *message) { m.Subaccounts[0].Sequence = nil },
		"bad collateral":   func(m *message) { m.Subaccounts[0].Collateral = "NaN" },
		"bad lots": func(m *message) {
			m.Subaccounts[0].Positions[0] = json.RawMessage(`{"symbol":"BTC","basePositionLots":"NaN"}`)
		},
		"duplicate position": func(m *message) {
			m.Subaccounts[0].Positions = append(m.Subaccounts[0].Positions, m.Subaccounts[0].Positions[0])
		},
		"bad spot": func(m *message) { m.Subaccounts[0].SpotCollaterals[0].Balance = "-1" },
	} {
		t.Run(name, func(t *testing.T) {
			s, generation, at := readyState(t)
			before := s.Snapshot().Cross
			m := snapshotMessage(t)
			mutate(&m)
			if err := s.apply(generation, m, at.Add(time.Second), true); err == nil {
				t.Fatal("accepted invalid account state")
			}
			after := s.Snapshot()
			if after.Synchronized || !after.PositionsUpdatedAt.IsZero() || !reflect.DeepEqual(before, after.Cross) {
				t.Fatal("invalid payload left authoritative or mutated state")
			}
		})
	}
}

func TestDeltaUpdatesOrdersCapabilitiesAndAuxiliaryRows(t *testing.T) {
	s, generation, at := readyState(t)
	m := deltaMessage(t)
	delta := &m.Deltas[0]
	delta.Positions = nil
	delta.SpotCollaterals = nil // Complete set: empty means SOL is no longer collateral.
	caps := s.Snapshot().Cross.Capabilities
	caps.State = "reduceOnly"
	caps.Actions.RiskIncreasingTrade.Immediate = false
	delta.Capabilities = &caps
	delta.Orders[0].Orders[0].Change = "updated"
	delta.Orders[0].Orders[0].SizeRemainingLots = "5"
	delta.Orders[0].Orders[0].Status = "open"
	delta.Splines = []json.RawMessage{json.RawMessage(`{"symbol":"BTC","change":"updated","spline":{"midPriceTicks":"61000","bidFilledAmountLots":"3","askFilledAmountLots":"4","askRegions":[]}}`)}
	if err := s.apply(generation, m, at.Add(time.Second), true); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot()
	if len(got.Cross.Positions) != 1 || got.Cross.Positions["BTC"].BasePositionLots != "-9007199254740993" ||
		got.Cross.Orders["BTC"]["18446744073709551615"].SizeRemainingLots != "5" || len(got.Cross.SpotCollaterals) != 0 ||
		got.Cross.Capabilities.State != "reduceOnly" || got.Cross.Capabilities.Actions.RiskIncreasingTrade.Immediate ||
		!got.Cross.Capabilities.Actions.RiskReducingTrade.Immediate {
		t.Fatal("delta did not preserve/update account resources")
	}
	var spline map[string]any
	if err := json.Unmarshal(got.Cross.Splines["BTC"], &spline); err != nil || spline["midPriceTicks"] != "61000" {
		t.Fatal("spline not updated")
	}
	// Missing optional capabilities retain the previous view, unlike the complete spot set.
	*m.Deltas[0].Sequence, *m.Slot = 43, 102
	m.Deltas[0].Capabilities = nil
	if err := s.apply(generation, m, at.Add(2*time.Second), true); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Cross.Capabilities.State != "reduceOnly" {
		t.Fatal("omitted capabilities erased account restrictions")
	}
}

func TestMalformedDeltaRowCannotMutateEarlierRows(t *testing.T) {
	for _, mutate := range []func(*message){
		func(m *message) { m.Deltas[0].Positions[1] = json.RawMessage(`{"symbol":"ETH","change":"updated"}`) },
		func(m *message) {
			m.Deltas[0].Splines = []json.RawMessage{json.RawMessage(`{"symbol":"BTC","change":"updated","spline":{}}`)}
		},
		func(m *message) { m.Deltas[0].Orders[0].Orders[0].OrderSequenceNumber = "-1" },
	} {
		s, generation, at := readyState(t)
		before := s.Snapshot().Cross
		m := deltaMessage(t)
		mutate(&m)
		if err := s.apply(generation, m, at.Add(time.Second), true); err == nil {
			t.Fatal("accepted malformed delta")
		}
		if got := s.Snapshot(); got.Synchronized || !reflect.DeepEqual(before, got.Cross) || got.Sequence != 41 {
			t.Fatal("bad delta partially changed exposure")
		}
	}
}
