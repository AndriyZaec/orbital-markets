package account

import (
	"fmt"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const (
	accountStateMaxAge     = 30 * time.Second
	leverageBracketsMaxAge = 5 * time.Minute
	maxSupportedLeverage   = 125
)

func ValidatePreTrade(snapshot AccountStateSnapshot, symbol string, marginRequired, leverage float64) []string {
	if !snapshot.Connected {
		return []string{"Aster account state not connected"}
	}
	if snapshot.LastUpdated.IsZero() || time.Since(snapshot.LastUpdated) > accountStateMaxAge {
		return []string{"Aster account state is stale"}
	}
	var blockers []string
	if !snapshot.OneWayModeKnown || !snapshot.OneWayMode {
		blockers = append(blockers, "Aster One-way Mode is required")
	}
	if !snapshot.CanTradeKnown || !snapshot.CanTrade {
		blockers = append(blockers, "Aster account is not allowed to trade")
	}
	if marginRequired > snapshot.Available*0.9 {
		blockers = append(blockers, fmt.Sprintf(
			"insufficient Aster margin with safety buffer: need $%.2f, available $%.2f",
			marginRequired, snapshot.Available,
		))
	}
	if leverage < domain.MinLeverage {
		blockers = append(blockers, fmt.Sprintf("leverage %.1fx below minimum %.0fx", leverage, domain.MinLeverage))
	}
	notional := marginRequired * leverage
	maximum, maximumKnown := FreshMaximumLeverage(snapshot, symbol, notional, time.Now())
	if !finiteNumber(notional) || notional < 0 {
		blockers = append(blockers, "invalid Aster planned notional")
	} else if !maximumKnown {
		blockers = append(blockers, fmt.Sprintf("Aster leverage brackets unavailable for %s", symbol))
	} else if leverage > float64(maximum) {
		blockers = append(blockers, fmt.Sprintf("Aster maximum leverage for %s is %dx", symbol, maximum))
	}
	return blockers
}

func FreshMaximumLeverage(snapshot AccountStateSnapshot, symbol string, notional float64, now time.Time) (int, bool) {
	updatedAt := snapshot.LeverageBracketsUpdatedAt[symbol]
	if updatedAt.IsZero() || now.Sub(updatedAt) > leverageBracketsMaxAge {
		return 0, false
	}
	return MaximumLeverage(snapshot.LeverageBrackets, symbol, notional)
}

func MaximumLeverage(brackets LeverageBrackets, symbol string, notional float64) (int, bool) {
	if !finiteNumber(notional) || notional < 0 {
		return 0, false
	}
	for _, bracket := range brackets[symbol] {
		if notional >= bracket.NotionalFloor && notional < bracket.NotionalCap {
			return min(int(bracket.InitialLeverage), maxSupportedLeverage), true
		}
	}
	return 0, false
}
