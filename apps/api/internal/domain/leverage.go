package domain

import "time"

// Shared leverage model.
//
// Leverage is user-selected and applied uniformly to both legs. Its upper
// bound comes from the selected asset's fresh venue metadata, not a global cap.
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
)

type LeverageCapabilityStatus string

const (
	LeverageCapabilityKnown       LeverageCapabilityStatus = "known"
	LeverageCapabilityPending     LeverageCapabilityStatus = "pending"
	LeverageCapabilityStale       LeverageCapabilityStatus = "stale"
	LeverageCapabilityMissing     LeverageCapabilityStatus = "missing"
	LeverageCapabilityUnsupported LeverageCapabilityStatus = "unsupported"
	LeverageCapabilityOutOfRange  LeverageCapabilityStatus = "out_of_range"
)

const (
	LeverageReasonAccountPending      = "account_pending"
	LeverageReasonAccountUnavailable  = "account_unavailable"
	LeverageReasonBracketMissing      = "bracket_missing"
	LeverageReasonBracketStale        = "bracket_stale"
	LeverageReasonNotionalOutOfRange  = "notional_out_of_range"
	LeverageReasonInvalidNotional     = "invalid_notional"
	LeverageReasonVenueUnsupported    = "venue_unsupported"
	LeverageReasonTargetRefreshFailed = "target_refresh_failed"
)

type LeverageCapability struct {
	Status            LeverageCapabilityStatus `json:"status"`
	Maximum           *int                     `json:"maximum,omitempty"`
	RequestedNotional float64                  `json:"requested_notional"`
	AccountRevision   uint64                   `json:"account_revision"`
	BracketRevision   uint64                   `json:"bracket_revision"`
	ObservedAt        time.Time                `json:"observed_at,omitempty"`
	ExpiresAt         time.Time                `json:"expires_at,omitempty"`
	Reason            string                   `json:"reason,omitempty"`
}

// LeverageConfig holds leverage-related sizing for a trade.
type LeverageConfig struct {
	Leverage          float64 `json:"leverage"`
	MarginRequired    float64 `json:"margin_required"`    // notional / leverage
	GrossExposure     float64 `json:"gross_exposure"`     // notional * 2 (both legs)
	EffectiveLeverage float64 `json:"effective_leverage"` // gross_exposure / margin_required
}

// ComputeLeverage builds a LeverageConfig from notional and leverage multiplier.
func ComputeLeverage(notional, leverage float64) LeverageConfig {
	if leverage <= 0 {
		leverage = DefaultLeverage
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

// ValidateLeverage returns true if leverage is within the current venue pair's
// supported range.
func ValidateLeverage(leverage, pairMax float64) bool {
	return pairMax >= MinLeverage && leverage >= MinLeverage && leverage <= pairMax
}
