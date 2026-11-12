package helper

import (
	"errors"
	"math"
	"strconv"
	"time"
)

const ProjectionQuoteMaxAge = 5 * time.Second

// OrderProjection contains exact execution-plan lots/ticks. It never accepts an
// arbitrary transaction or a caller-supplied fee amount. Account adapters replace
// TakerFeeMultiplier from the captured trader state before calling the helper.
type OrderProjection struct {
	IntentID            string  `json:"intent_id"`
	Symbol              string  `json:"symbol"`
	Side                string  `json:"side"`
	NumBaseLots         string  `json:"num_base_lots"`
	PriceLimitTicks     string  `json:"price_limit_ticks"`
	QuoteObservedUnixMS int64   `json:"quote_observed_unix_ms"`
	TakerFeeMultiplier  float64 `json:"taker_fee_multiplier"`
	FlightFeeBPS        uint16  `json:"flight_fee_bps"`
	ReduceOnly          bool    `json:"reduce_only"`
}

type ProjectionResult struct {
	IntentID                              string `json:"intent_id"`
	Symbol                                string `json:"symbol"`
	Side                                  string `json:"side"`
	NumBaseLots                           string `json:"num_base_lots"`
	PriceLimitTicks                       string `json:"price_limit_ticks"`
	FlightFeeBPS                          uint16 `json:"flight_fee_bps"`
	Model                                 string `json:"model"`
	FeePolicy                             string `json:"fee_policy"`
	NotionalQuoteLots                     string `json:"notional_quote_lots"`
	ExchangeFeeQuoteLots                  string `json:"exchange_fee_quote_lots"`
	FlightFeeQuoteLots                    string `json:"flight_fee_quote_lots"`
	TotalFeeQuoteLots                     string `json:"total_fee_quote_lots"`
	ProjectedBaseLots                     string `json:"projected_base_lots"`
	ProjectedEquityQuoteLots              string `json:"projected_equity_quote_lots"`
	ProjectedEffectiveCollateralQuoteLots string `json:"projected_effective_collateral_quote_lots"`
	ProjectedInitialMarginQuoteLots       string `json:"projected_initial_margin_quote_lots"`
	ProjectedMaintenanceMarginQuoteLots   string `json:"projected_maintenance_margin_quote_lots"`
	ProjectedAvailableMarginQuoteLots     string `json:"projected_available_margin_quote_lots"`
	ProjectedRiskTier                     string `json:"projected_risk_tier"`
	MarginSufficient                      bool   `json:"margin_sufficient"`
	Reason                                string `json:"reason"`
}

func ValidateProjectionIntent(order OrderProjection) error {
	if order.IntentID == "" || len(order.IntentID) > 128 || order.Symbol == "" || (order.Side != "bid" && order.Side != "ask") || order.ReduceOnly || order.FlightFeeBPS != 2 || math.IsNaN(order.TakerFeeMultiplier) || math.IsInf(order.TakerFeeMultiplier, 0) || order.TakerFeeMultiplier < 0 {
		return errors.New("invalid Phoenix open projection intent")
	}
	for _, value := range []string{order.NumBaseLots, order.PriceLimitTicks} {
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil || parsed == 0 {
			return errors.New("invalid Phoenix projected lots or ticks")
		}
	}
	return nil
}

func validateProjectionResult(q RiskRequest, r RiskResult) error {
	if q.Projection == nil {
		if r.Projection != nil {
			return errors.New("unexpected Phoenix order projection")
		}
		return nil
	}
	order, p := q.Projection, r.Projection
	if p == nil || p.IntentID != order.IntentID || p.Symbol != order.Symbol || p.Side != order.Side || p.NumBaseLots != order.NumBaseLots || p.PriceLimitTicks != order.PriceLimitTicks || p.FlightFeeBPS != 2 || p.Model != "rise_full_fill_at_limit" || p.FeePolicy != "undiscounted_taker_ceiling" {
		return errors.New("Phoenix projection does not match order intent")
	}
	for _, value := range []string{p.ProjectedBaseLots, p.ProjectedEquityQuoteLots, p.ProjectedEffectiveCollateralQuoteLots} {
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return errors.New("invalid signed projection amount")
		}
	}
	for _, value := range []string{p.NotionalQuoteLots, p.ExchangeFeeQuoteLots, p.FlightFeeQuoteLots, p.TotalFeeQuoteLots, p.ProjectedInitialMarginQuoteLots, p.ProjectedMaintenanceMarginQuoteLots, p.ProjectedAvailableMarginQuoteLots} {
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return errors.New("invalid unsigned projection amount")
		}
	}
	exchange, _ := strconv.ParseUint(p.ExchangeFeeQuoteLots, 10, 64)
	flight, _ := strconv.ParseUint(p.FlightFeeQuoteLots, 10, 64)
	total, _ := strconv.ParseUint(p.TotalFeeQuoteLots, 10, 64)
	if exchange > math.MaxUint64-flight || total != exchange+flight {
		return errors.New("inconsistent Phoenix projected fee components")
	}
	switch p.ProjectedRiskTier {
	case "Safe", "AtRisk", "Cancellable", "Liquidatable", "BackstopLiquidatable", "HighRisk":
	default:
		return errors.New("unknown projected risk tier")
	}
	if p.MarginSufficient && (p.Reason != "" || p.ProjectedRiskTier != "Safe" || r.RiskTier != "Safe") {
		return errors.New("invalid successful projection decision")
	}
	if !p.MarginSufficient {
		switch p.Reason {
		case "current_account_not_safe", "projected_account_not_safe", "insufficient_margin_with_buffer":
		default:
			return errors.New("missing Phoenix projection blocker")
		}
	}
	return nil
}
