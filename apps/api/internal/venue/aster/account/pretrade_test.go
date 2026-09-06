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
		Available: 100, LeverageBrackets: LeverageBrackets{"BTCUSDT": {
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
		LeverageBrackets: LeverageBrackets{"BTCUSDT": {
			{InitialLeverage: 10, NotionalFloor: 0, NotionalCap: 1000},
		}},
	}, "BTCUSDT", 50, 5)
	if len(reasons) != 0 {
		t.Fatalf("reasons = %v", reasons)
	}
}
