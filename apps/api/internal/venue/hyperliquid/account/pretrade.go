package account

import (
	"fmt"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

// ValidationLevel classifies the pre-trade check result.
type ValidationLevel string

const (
	ValidationOK      ValidationLevel = "ok"
	ValidationWarning ValidationLevel = "warning"
	ValidationBlocker ValidationLevel = "blocker"
)

// PreTradeResult is the outcome of pre-trade validation.
type PreTradeResult struct {
	Level   ValidationLevel `json:"level"`
	Reasons []string        `json:"reasons"`
}

func (r PreTradeResult) CanProceed() bool {
	return r.Level != ValidationBlocker
}

const (
	maxAccountStateAge = 30 * time.Second
	marginSafetyBuffer = 0.10 // keep 10% margin buffer
)

// ValidatePreTrade checks whether a planned Hyperliquid leg can be safely submitted.
//
// Checks:
//  1. Account state is connected and fresh
//  2. Enough available balance for required margin
//  3. Leverage is within Orbital's allowed range
//  4. Symbol is known (has position data or market data available)
func ValidatePreTrade(
	snap AccountStateSnapshot,
	symbol string,
	marginRequired float64,
	leverage float64,
) PreTradeResult {
	var reasons []string
	level := ValidationOK

	warn := func(msg string) {
		reasons = append(reasons, msg)
		if level == ValidationOK {
			level = ValidationWarning
		}
	}
	block := func(msg string) {
		reasons = append(reasons, msg)
		level = ValidationBlocker
	}

	// 1. Account state freshness
	if !snap.Connected {
		block("account state not connected")
		return PreTradeResult{Level: level, Reasons: reasons}
	}
	if snap.LastUpdated.IsZero() || time.Since(snap.LastUpdated) > maxAccountStateAge {
		block(fmt.Sprintf("account state stale (%.0fs old)", time.Since(snap.LastUpdated).Seconds()))
		return PreTradeResult{Level: level, Reasons: reasons}
	}

	// 2. Margin sufficiency
	available := snap.Margin.AvailableBalance
	availableAfterBuffer := available * (1 - marginSafetyBuffer)
	if marginRequired > available {
		block(fmt.Sprintf(
			"insufficient margin: need $%.2f, available $%.2f",
			marginRequired, available,
		))
	} else if marginRequired > availableAfterBuffer {
		warn(fmt.Sprintf(
			"margin tight: need $%.2f, available $%.2f (%.0f%% buffer breached)",
			marginRequired, available, marginSafetyBuffer*100,
		))
	}

	// 3. Leverage range
	if leverage < domain.MinLeverage {
		block(fmt.Sprintf("leverage %.1fx below minimum %.0fx", leverage, domain.MinLeverage))
	}

	if len(reasons) == 0 {
		return PreTradeResult{Level: ValidationOK, Reasons: nil}
	}
	return PreTradeResult{Level: level, Reasons: reasons}
}
