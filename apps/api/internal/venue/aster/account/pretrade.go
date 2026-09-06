package account

import (
	"fmt"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const accountStateMaxAge = 30 * time.Second

func ValidatePreTrade(snapshot AccountStateSnapshot, symbol string, marginRequired, leverage float64) []string {
	if !snapshot.Connected {
		return []string{"browser-assisted account state not connected"}
	}
	if snapshot.LastUpdated.IsZero() || time.Since(snapshot.LastUpdated) > accountStateMaxAge {
		return []string{"browser-assisted account state is stale"}
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
	maximum := 0.0
	for _, bracket := range snapshot.LeverageBrackets[symbol] {
		if notional >= bracket.NotionalFloor && notional < bracket.NotionalCap {
			maximum = bracket.InitialLeverage
			break
		}
	}
	if !finiteNumber(notional) || notional < 0 {
		blockers = append(blockers, "invalid Aster planned notional")
	} else if maximum == 0 {
		blockers = append(blockers, fmt.Sprintf("Aster leverage brackets unavailable for %s", symbol))
	} else if leverage > maximum {
		blockers = append(blockers, fmt.Sprintf("Aster maximum leverage for %s is %.0fx", symbol, maximum))
	}
	return blockers
}
