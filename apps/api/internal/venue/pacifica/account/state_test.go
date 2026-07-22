package account

import "testing"

func TestAccountStateReset(t *testing.T) {
	state := NewAccountState()
	state.SetConnected(true)
	state.UpdateEquity(46, 40, 35, 6, 2)
	state.UpdateSymbolConfig(SymbolConfig{Symbol: "SOL", Leverage: 5, MarginMode: MarginModeCross})

	state.Reset()
	snap := state.Snapshot()
	if snap.Connected || !snap.LastUpdated.IsZero() || snap.Equity != 0 || snap.AvailableToSpend != 0 {
		t.Fatalf("Reset() left account state populated: %+v", snap)
	}
	if len(snap.SymbolConfigs) != 0 || len(snap.Positions) != 0 {
		t.Fatalf("Reset() left account collections populated: %+v", snap)
	}
}

func TestAccountStateRejectsLateUpdatesFromPreviousAccount(t *testing.T) {
	state := NewAccountState()
	state.ResetForAccount("new-wallet")
	state.UpdatePositionsForAccount("old-wallet", []AccountPosition{{Symbol: "SOL", Size: 10}})
	state.SetConnectedForAccount("old-wallet", true)

	snap := state.Snapshot()
	if snap.Account != "new-wallet" || snap.Connected || len(snap.Positions) != 0 || !snap.PositionsUpdatedAt.IsZero() {
		t.Fatalf("old account update contaminated state: %+v", snap)
	}
}
