package account

import "testing"

func TestAccountStateReset(t *testing.T) {
	state := NewAccountState()
	state.SetConnected(true)
	state.UpdateMargin(MarginSummary{AccountEquity: 99.6, AvailableBalance: 99.6})
	state.UpdatePositions([]AssetPosition{{Coin: "SOL", Size: 1}})

	state.Reset()
	snap := state.Snapshot()
	if snap.Connected || !snap.LastUpdated.IsZero() || snap.Margin.AccountEquity != 0 || snap.Margin.AvailableBalance != 0 {
		t.Fatalf("Reset() left account state populated: %+v", snap)
	}
	if len(snap.Positions) != 0 {
		t.Fatalf("Reset() left positions populated: %+v", snap)
	}
}

func TestAccountStateRejectsLateUpdatesFromPreviousAccount(t *testing.T) {
	state := NewAccountState()
	state.ResetForAccount("0xnew")
	state.UpdatePositionsForAccount("0xold", []AssetPosition{{Coin: "SOL", Size: 10}})
	state.SetConnectedForAccount("0xold", true)

	snap := state.Snapshot()
	if snap.Account != "0xnew" || snap.Connected || len(snap.Positions) != 0 || !snap.PositionsUpdatedAt.IsZero() {
		t.Fatalf("old account update contaminated state: %+v", snap)
	}
}
