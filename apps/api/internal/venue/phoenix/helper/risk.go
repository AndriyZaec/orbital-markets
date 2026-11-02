package helper

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"
)

const (
	RiskDataMaxAge   = 30 * time.Second
	RiskParamsMaxAge = 5 * time.Minute
)

type RiskRequest struct {
	OwnerAuthority        string            `json:"owner_authority"`
	TraderPDAIndex        uint8             `json:"trader_pda_index"`
	SubaccountIndex       uint8             `json:"subaccount_index"`
	AccountGeneration     uint64            `json:"account_generation"`
	AccountSlot           uint64            `json:"account_slot"`
	AccountSequence       uint64            `json:"account_sequence"`
	MetadataGeneration    uint64            `json:"metadata_generation"`
	AsOfUnixMS            int64             `json:"as_of_unix_ms"`
	AccountObservedUnixMS int64             `json:"account_observed_unix_ms"`
	ParamsObservedUnixMS  int64             `json:"params_observed_unix_ms"`
	CollateralQuoteLots   string            `json:"collateral_quote_lots"`
	HasSplines            bool              `json:"has_splines"`
	Positions             []RiskPosition    `json:"positions"`
	Orders                []RiskOrder       `json:"orders"`
	SpotBalances          []RiskSpotBalance `json:"spot_balances"`
	Markets               []RiskMarket      `json:"markets"`
	SpotParams            []RiskSpotParams  `json:"spot_params"`
}

type RiskPosition struct {
	Symbol                      string `json:"symbol"`
	Sequence                    string `json:"sequence"`
	BaseLots                    string `json:"base_lots"`
	VirtualQuoteLots            string `json:"virtual_quote_lots"`
	UnsettledFundingQuoteLots   string `json:"unsettled_funding_quote_lots"`
	AccumulatedFundingQuoteLots string `json:"accumulated_funding_quote_lots"`
}

type RiskOrder struct {
	Symbol            string `json:"symbol"`
	Sequence          string `json:"sequence"`
	Side              string `json:"side"`
	PriceTicks        string `json:"price_ticks"`
	RemainingBaseLots string `json:"remaining_base_lots"`
	InitialBaseLots   string `json:"initial_base_lots"`
	ReduceOnly        bool   `json:"reduce_only"`
	IsStopLoss        bool   `json:"is_stop_loss"`
}

type RiskSpotBalance struct {
	AssetIndex uint32 `json:"asset_index"`
	Symbol     string `json:"symbol"`
	Balance    string `json:"balance"`
	Decimals   uint8  `json:"decimals"`
}

type RiskSpotParams struct {
	AssetIndex           uint32 `json:"asset_index"`
	Symbol               string `json:"symbol"`
	PricingMarketSymbol  string `json:"pricing_market_symbol"`
	Decimals             uint8  `json:"decimals"`
	MaxGlobalBalance     string `json:"max_global_balance"`
	MinMarginDiscountBPS uint16 `json:"min_margin_discount_bps"`
	MaxMarginDiscountBPS uint16 `json:"max_margin_discount_bps"`
}

// RiskMarket must come from full venue risk parameters and fresh mark/index
// inputs, not the scanner's simplified MaxLeverage or a default tier table.
type RiskMarket struct {
	Symbol                      string           `json:"symbol"`
	AssetID                     uint64           `json:"asset_id"`
	BaseLotDecimals             int8             `json:"base_lot_decimals"`
	TickSize                    string           `json:"tick_size"`
	MarkPriceTicks              string           `json:"mark_price_ticks"`
	IndexPriceTicks             *string          `json:"index_price_ticks"`
	PriceObservedUnixMS         int64            `json:"price_observed_unix_ms"`
	LeverageTiers               [4]RiskTierInput `json:"leverage_tiers"`
	RiskFactorsBPS              [3]uint16        `json:"risk_factors_bps"`
	CancelOrderRiskFactorBPS    uint16           `json:"cancel_order_risk_factor_bps"`
	UPnLRiskFactorBPS           uint16           `json:"upnl_risk_factor_bps"`
	UPnLWithdrawalRiskFactorBPS uint16           `json:"upnl_withdrawal_risk_factor_bps"`
}

type RiskTierInput struct {
	UpperBoundBaseLots      string `json:"upper_bound_base_lots"`
	MaxLeverage             uint64 `json:"max_leverage"`
	LimitOrderRiskFactorBPS uint16 `json:"limit_order_risk_factor_bps"`
}

type RiskResult struct {
	OwnerAuthority               string              `json:"owner_authority"`
	TraderPDAIndex               uint8               `json:"trader_pda_index"`
	SubaccountIndex              uint8               `json:"subaccount_index"`
	AccountGeneration            uint64              `json:"account_generation"`
	AccountSlot                  uint64              `json:"account_slot"`
	AccountSequence              uint64              `json:"account_sequence"`
	MetadataGeneration           uint64              `json:"metadata_generation"`
	AsOfUnixMS                   int64               `json:"as_of_unix_ms"`
	ValidUntilUnixMS             int64               `json:"valid_until_unix_ms"`
	QuoteDecimals                uint8               `json:"quote_decimals"`
	EquityQuoteLots              string              `json:"equity_quote_lots"`
	EffectiveCollateralQuoteLots string              `json:"effective_collateral_quote_lots"`
	AvailableMarginQuoteLots     string              `json:"available_margin_quote_lots"`
	InitialMarginQuoteLots       string              `json:"initial_margin_quote_lots"`
	MaintenanceMarginQuoteLots   string              `json:"maintenance_margin_quote_lots"`
	UnsettledFundingQuoteLots    string              `json:"unsettled_funding_quote_lots"`
	UnrealizedPnLQuoteLots       string              `json:"unrealized_pnl_quote_lots"`
	SpotNotionalQuoteLots        string              `json:"spot_notional_quote_lots"`
	DiscountedSpotQuoteLots      string              `json:"discounted_spot_quote_lots"`
	RiskTier                     string              `json:"risk_tier"`
	LiquidationModel             string              `json:"liquidation_model"`
	StaticLiquidationPricesUSD   map[string]*float64 `json:"static_liquidation_prices_usd"`
}

func (c *Client) ComputeRisk(ctx context.Context, request RiskRequest) (RiskResult, error) {
	if _, err := RiskExpiry(request); err != nil {
		return RiskResult{}, err
	}
	var result RiskResult
	if err := c.call(ctx, "compute_risk", request, &result); err != nil {
		return RiskResult{}, err
	}
	if err := ValidateRiskResult(request, result, time.Now()); err != nil {
		return RiskResult{}, err
	}
	return result, nil
}

// RiskExpiry binds output validity to every observation used in the calculation.
// It is independent of RPC/blockhash freshness and never extends an input's TTL.
func RiskExpiry(q RiskRequest) (int64, error) {
	if q.TraderPDAIndex != 0 || q.SubaccountIndex != 0 || q.AccountGeneration == 0 || q.MetadataGeneration == 0 || q.AsOfUnixMS <= 0 {
		return 0, errors.New("invalid Phoenix risk account context")
	}
	expires := int64(math.MaxInt64)
	check := func(observed int64, age time.Duration) error {
		ms := age.Milliseconds()
		if observed <= 0 || observed > q.AsOfUnixMS || q.AsOfUnixMS-observed > ms || observed > math.MaxInt64-ms {
			return errors.New("Phoenix risk inputs missing, future-dated or stale")
		}
		expires = min(expires, observed+ms)
		return nil
	}
	if err := check(q.AccountObservedUnixMS, RiskDataMaxAge); err != nil {
		return 0, err
	}
	if err := check(q.ParamsObservedUnixMS, RiskParamsMaxAge); err != nil {
		return 0, err
	}
	for _, market := range q.Markets {
		if err := check(market.PriceObservedUnixMS, RiskDataMaxAge); err != nil {
			return 0, err
		}
	}
	return expires, nil
}

func ValidateRiskResult(q RiskRequest, r RiskResult, now time.Time) error {
	expires, err := RiskExpiry(q)
	if err != nil {
		return err
	}
	if r.OwnerAuthority != q.OwnerAuthority || r.TraderPDAIndex != 0 || r.SubaccountIndex != 0 ||
		r.AccountGeneration != q.AccountGeneration || r.AccountSlot != q.AccountSlot || r.AccountSequence != q.AccountSequence ||
		r.MetadataGeneration != q.MetadataGeneration || r.AsOfUnixMS != q.AsOfUnixMS ||
		r.ValidUntilUnixMS != expires || now.UnixMilli() > expires || now.UnixMilli() < q.AsOfUnixMS || r.QuoteDecimals != 6 ||
		r.LiquidationModel != "rise_local_static" {
		return errors.New("Phoenix risk result context, units or validity mismatch")
	}
	for _, value := range []string{r.EquityQuoteLots, r.EffectiveCollateralQuoteLots, r.UnsettledFundingQuoteLots, r.UnrealizedPnLQuoteLots} {
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return errors.New("invalid signed Phoenix risk result")
		}
	}
	for _, value := range []string{r.AvailableMarginQuoteLots, r.InitialMarginQuoteLots, r.MaintenanceMarginQuoteLots, r.SpotNotionalQuoteLots, r.DiscountedSpotQuoteLots} {
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return errors.New("invalid unsigned Phoenix risk result")
		}
	}
	switch r.RiskTier {
	case "Safe", "AtRisk", "Cancellable", "Liquidatable", "BackstopLiquidatable", "HighRisk":
	default:
		return errors.New("unknown Phoenix SDK risk tier")
	}
	expected := make(map[string]bool)
	for _, position := range q.Positions {
		base, err := strconv.ParseInt(position.BaseLots, 10, 64)
		if err != nil {
			return err
		}
		if base != 0 {
			expected[position.Symbol] = true
		}
	}
	if len(expected) != len(r.StaticLiquidationPricesUSD) {
		return errors.New("Phoenix liquidation result omitted exposure")
	}
	for symbol, price := range r.StaticLiquidationPricesUSD {
		if !expected[symbol] || (price != nil && (math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0)) {
			return errors.New("invalid Phoenix liquidation result")
		}
	}
	return nil
}
