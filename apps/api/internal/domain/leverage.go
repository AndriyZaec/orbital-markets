package domain

// V1 leverage model.
//
// Leverage is user-selected, applied uniformly to both legs.
// margin_required = notional / leverage
// gross_exposure = notional * 2 (both legs)
// effective_leverage = gross_exposure / margin_required
//
// Example at 3x:
//   notional = $3,000
//   margin_required = $1,000
//   gross_exposure = $6,000 (long $3K + short $3K)
//   effective_leverage = 6x (exposure / margin)

const (
	DefaultLeverage = 1.0
	MinLeverage     = 1.0
	MaxLeverage     = 5.0
)

// LeverageConfig holds leverage-related sizing for a trade.
type LeverageConfig struct {
	Leverage        float64 `json:"leverage"`         // user-selected, 1x-5x
	MarginRequired  float64 `json:"margin_required"`  // notional / leverage
	GrossExposure   float64 `json:"gross_exposure"`   // notional * 2 (both legs)
	EffectiveLeverage float64 `json:"effective_leverage"` // gross_exposure / margin_required
}

// ComputeLeverage builds a LeverageConfig from notional and leverage multiplier.
func ComputeLeverage(notional, leverage float64) LeverageConfig {
	if leverage < MinLeverage {
		leverage = MinLeverage
	}
	if leverage > MaxLeverage {
		leverage = MaxLeverage
	}

	margin := notional / leverage
	exposure := notional * 2

	return LeverageConfig{
		Leverage:          leverage,
		MarginRequired:    margin,
		GrossExposure:     exposure,
		EffectiveLeverage: exposure / margin,
	}
}

// ValidateLeverage returns true if leverage is within allowed range.
func ValidateLeverage(leverage float64) bool {
	return leverage >= MinLeverage && leverage <= MaxLeverage
}
