package account

import (
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
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

func TestLeverageCapabilityDistinguishesUnknownStates(t *testing.T) {
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		snapshot AccountStateSnapshot
		notional float64
		want     domain.LeverageCapabilityStatus
	}{
		{name: "pending", snapshot: AccountStateSnapshot{}, notional: 100, want: domain.LeverageCapabilityPending},
		{name: "missing", snapshot: AccountStateSnapshot{Connected: true, LastUpdated: now}, notional: 100, want: domain.LeverageCapabilityMissing},
		{name: "stale", snapshot: AccountStateSnapshot{
			Connected: true, LastUpdated: now,
			LeverageBracketsUpdatedAt: map[string]time.Time{"PIPPINUSDT": now.Add(-leverageBracketsMaxAge - time.Second)},
			LeverageBrackets:          LeverageBrackets{"PIPPINUSDT": {{InitialLeverage: 10, NotionalCap: 1000}}},
		}, notional: 100, want: domain.LeverageCapabilityStale},
		{name: "out of range", snapshot: AccountStateSnapshot{
			Connected: true, LastUpdated: now,
			LeverageBracketsUpdatedAt: map[string]time.Time{"PIPPINUSDT": now},
			LeverageBrackets:          LeverageBrackets{"PIPPINUSDT": {{InitialLeverage: 10, NotionalCap: 1000}}},
		}, notional: 1000, want: domain.LeverageCapabilityOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capability := LeverageCapability(tt.snapshot, "PIPPINUSDT", tt.notional, now)
			if capability.Status != tt.want || capability.Maximum != nil {
				t.Fatalf("capability = %+v, want status %s without maximum", capability, tt.want)
			}
		})
	}
}

func TestLeverageCapabilityIncludesKnownRevisionsAndExpiry(t *testing.T) {
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	accountObservedAt := now.Add(-time.Second)
	bracketObservedAt := now.Add(-2 * time.Second)
	capability := LeverageCapability(AccountStateSnapshot{
		Connected: true, LastUpdated: accountObservedAt,
		LeverageBracketsUpdatedAt: map[string]time.Time{"PIPPINUSDT": bracketObservedAt},
		LeverageBrackets:          LeverageBrackets{"PIPPINUSDT": {{InitialLeverage: 10, NotionalCap: 1000}}},
	}, "PIPPINUSDT", 100, now)
	if capability.Status != domain.LeverageCapabilityKnown || capability.Maximum == nil || *capability.Maximum != 10 {
		t.Fatalf("capability = %+v, want known 10x", capability)
	}
	if capability.AccountRevision != uint64(accountObservedAt.UnixMilli()) || capability.BracketRevision != uint64(bracketObservedAt.UnixMilli()) ||
		!capability.ObservedAt.Equal(bracketObservedAt) || !capability.ExpiresAt.Equal(bracketObservedAt.Add(leverageBracketsMaxAge)) {
		t.Fatalf("capability revisions = %+v", capability)
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
