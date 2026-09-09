package account

import (
	"strings"
	"testing"
	"time"
)

func TestValidatePreTradeRequiresOneWayModeAndLeverageBrackets(t *testing.T) {
	snapshot := AccountStateSnapshot{
		Connected: true, LastUpdated: time.Now(), OneWayModeKnown: true,
		OneWayMode: false, CanTradeKnown: true, CanTrade: true,
		Available: 100, LeverageBracketsUpdatedAt: map[string]time.Time{"BTCUSDT": time.Now()}, LeverageBrackets: LeverageBrackets{"BTCUSDT": {
			{InitialLeverage: 5, NotionalFloor: 0, NotionalCap: 1000},
		}},
	}
	reasons := ValidatePreTrade(snapshot, "BTCUSDT", 10, 6)
	joined := strings.Join(reasons, "; ")
	if !strings.Contains(joined, "One-way Mode") || !strings.Contains(joined, "maximum leverage") {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestValidatePreTradeAcceptsFreshTradingAccount(t *testing.T) {
	reasons := ValidatePreTrade(AccountStateSnapshot{
		Connected: true, LastUpdated: time.Now(), OneWayModeKnown: true, OneWayMode: true,
		CanTradeKnown: true, CanTrade: true, Available: 100,
		LeverageBracketsUpdatedAt: map[string]time.Time{"BTCUSDT": time.Now()},
		LeverageBrackets: LeverageBrackets{"BTCUSDT": {
			{InitialLeverage: 10, NotionalFloor: 0, NotionalCap: 1000},
		}},
	}, "BTCUSDT", 50, 5)
	if len(reasons) != 0 {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestValidatePreTradeRejectsStaleLeverageBrackets(t *testing.T) {
	reasons := ValidatePreTrade(AccountStateSnapshot{
		Connected: true, LastUpdated: time.Now(), OneWayModeKnown: true, OneWayMode: true,
		CanTradeKnown: true, CanTrade: true, Available: 100,
		LeverageBracketsUpdatedAt: map[string]time.Time{"MEMEUSDT": time.Now().Add(-leverageBracketsMaxAge - time.Second)},
		LeverageBrackets: LeverageBrackets{"MEMEUSDT": {
			{InitialLeverage: 20, NotionalFloor: 0, NotionalCap: 10000},
		}},
	}, "MEMEUSDT", 50, 5)
	if joined := strings.Join(reasons, "; "); !strings.Contains(joined, "leverage brackets unavailable") {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestMaximumLeverageUsesNotionalBracket(t *testing.T) {
	brackets := LeverageBrackets{"MEMEUSDT": {
		{InitialLeverage: 20, NotionalFloor: 0, NotionalCap: 1000},
		{InitialLeverage: 10, NotionalFloor: 1000, NotionalCap: 10000},
	}}

	if maximum, found := MaximumLeverage(brackets, "MEMEUSDT", 999); !found || maximum != 20 {
		t.Fatalf("maximum at 999 = %d, %v, want 20, true", maximum, found)
	}
	if maximum, found := MaximumLeverage(brackets, "MEMEUSDT", 1000); !found || maximum != 10 {
		t.Fatalf("maximum at 1000 = %d, %v, want 10, true", maximum, found)
	}
	if _, found := MaximumLeverage(brackets, "MEMEUSDT", 10000); found {
		t.Fatal("maximum at upper cap found, want unavailable")
	}
}

func TestApplyLeverageBracketsKeepsNewestSymbolGeneration(t *testing.T) {
	state := NewAccountState("0xowner")
	newer := time.Now()
	state.ApplyLeverageBrackets(LeverageBrackets{"MEMEUSDT": {
		{InitialLeverage: 10, NotionalFloor: 0, NotionalCap: 10000},
	}}, newer)
	state.ApplyLeverageBrackets(LeverageBrackets{
		"MEMEUSDT": {{InitialLeverage: 20, NotionalFloor: 0, NotionalCap: 10000}},
		"BTCUSDT":  {{InitialLeverage: 50, NotionalFloor: 0, NotionalCap: 10000}},
	}, newer.Add(-time.Second))

	snapshot := state.Snapshot()
	if maximum, found := FreshMaximumLeverage(snapshot, "MEMEUSDT", 100, newer); !found || maximum != 10 {
		t.Fatalf("MEME maximum = %d, %v, want 10, true", maximum, found)
	}
	if maximum, found := FreshMaximumLeverage(snapshot, "BTCUSDT", 100, newer); !found || maximum != 50 {
		t.Fatalf("BTC maximum = %d, %v, want 50, true", maximum, found)
	}
}
