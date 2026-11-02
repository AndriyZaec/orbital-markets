//! Read-only adapter to pinned Rise math. No RPC, signer or order simulation.
use std::collections::{BTreeMap, HashMap, HashSet};
use std::str::FromStr;

use phoenix_rise::math::portfolio::{
    MarginSimulationAction, SimulateMarginParams, SpotCollateralInput,
};
use phoenix_rise::math::{
    BaseLots, BasisPoints, Constant, LeverageTier, LeverageTiers, LimitOrder, MarketCalculator,
    PerpAssetMetadata, QuoteLotsPerBaseLotPerTick, SequenceNumberU8, Side, SignedBaseLots,
    SignedQuoteLots, SignedQuoteLotsI56, SignedQuoteLotsPerBaseLot, Ticks, TraderPortfolio,
    TraderPosition,
};
use serde::{Deserialize, Serialize};
use solana_pubkey::Pubkey;

const DATA_MAX_AGE_MS: u64 = 30_000;
const PARAMS_MAX_AGE_MS: u64 = 300_000;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RiskRequest {
    owner_authority: String,
    trader_pda_index: u8,
    subaccount_index: u8,
    account_generation: u64,
    account_slot: u64,
    account_sequence: u64,
    metadata_generation: u64,
    as_of_unix_ms: u64,
    account_observed_unix_ms: u64,
    params_observed_unix_ms: u64,
    collateral_quote_lots: String,
    has_splines: bool,
    positions: Vec<PositionInput>,
    orders: Vec<OrderInput>,
    spot_balances: Vec<SpotBalance>,
    markets: Vec<MarketInput>,
    spot_params: Vec<SpotParams>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PositionInput {
    symbol: String,
    sequence: String,
    base_lots: String,
    virtual_quote_lots: String,
    unsettled_funding_quote_lots: String,
    accumulated_funding_quote_lots: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct OrderInput {
    symbol: String,
    sequence: String,
    side: String,
    price_ticks: String,
    remaining_base_lots: String,
    initial_base_lots: String,
    reduce_only: bool,
    is_stop_loss: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SpotBalance {
    asset_index: u32,
    symbol: String,
    balance: String,
    decimals: u8,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SpotParams {
    asset_index: u32,
    symbol: String,
    pricing_market_symbol: String,
    decimals: u8,
    max_global_balance: String,
    min_margin_discount_bps: u16,
    max_margin_discount_bps: u16,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MarketInput {
    symbol: String,
    asset_id: u64,
    base_lot_decimals: i8,
    tick_size: String,
    #[serde(default)]
    mark_price_ticks: Option<String>,
    index_price_ticks: Option<String>,
    mark_price_usd: Option<f64>,
    index_price_usd: Option<f64>,
    price_observed_unix_ms: u64,
    leverage_tiers: [TierInput; 4],
    risk_factors_bps: [u16; 3],
    cancel_order_risk_factor_bps: u16,
    upnl_risk_factor_bps: u16,
    upnl_withdrawal_risk_factor_bps: u16,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TierInput {
    upper_bound_base_lots: String,
    max_leverage: u64,
    limit_order_risk_factor_bps: u16,
}

#[derive(Serialize)]
pub struct RiskResult {
    owner_authority: String,
    trader_pda_index: u8,
    subaccount_index: u8,
    account_generation: u64,
    account_slot: u64,
    account_sequence: u64,
    metadata_generation: u64,
    as_of_unix_ms: u64,
    valid_until_unix_ms: u64,
    quote_decimals: u8,
    equity_quote_lots: String,
    effective_collateral_quote_lots: String,
    available_margin_quote_lots: String,
    initial_margin_quote_lots: String,
    maintenance_margin_quote_lots: String,
    unsettled_funding_quote_lots: String,
    unrealized_pnl_quote_lots: String,
    spot_notional_quote_lots: String,
    discounted_spot_quote_lots: String,
    risk_tier: String,
    liquidation_model: &'static str,
    static_liquidation_prices_usd: BTreeMap<String, Option<f64>>,
}

pub fn compute(request: RiskRequest) -> Result<RiskResult, String> {
    let owner =
        Pubkey::from_str(&request.owner_authority).map_err(|_| "invalid owner authority")?;
    if request.trader_pda_index != 0 || request.subaccount_index != 0 {
        return Err("risk only supports cross trader 0/0".into());
    }
    if request.account_generation == 0 || request.metadata_generation == 0 {
        return Err("account and metadata generations are required".into());
    }
    if request.has_splines {
        return Err("spline exposure requires Hawkeye risk views".into());
    }
    let mut valid_until = expiry(
        request.as_of_unix_ms,
        request.account_observed_unix_ms,
        DATA_MAX_AGE_MS,
    )?;
    valid_until = valid_until.min(expiry(
        request.as_of_unix_ms,
        request.params_observed_unix_ms,
        PARAMS_MAX_AGE_MS,
    )?);
    let mut provider = HashMap::new();
    let mut indices = HashMap::new();
    let mut asset_ids = HashSet::new();
    for market in &request.markets {
        valid_until = valid_until.min(expiry(
            request.as_of_unix_ms,
            market.price_observed_unix_ms,
            DATA_MAX_AGE_MS,
        )?);
        if market.symbol.is_empty()
            || provider.contains_key(&market.symbol)
            || !asset_ids.insert(market.asset_id)
        {
            return Err("duplicate or missing market identity".into());
        }
        if !(-18..=18).contains(&market.base_lot_decimals) {
            return Err("invalid base lot decimals".into());
        }
        let tick_size = uint(&market.tick_size)?;
        if tick_size == 0 {
            return Err("tick size must be positive".into());
        }
        let calculator = MarketCalculator::new(
            market.base_lot_decimals,
            QuoteLotsPerBaseLotPerTick::new(tick_size),
        );
        let mark = price_ticks(
            market.mark_price_ticks.as_deref(),
            market.mark_price_usd,
            &calculator,
        )?
        .ok_or("missing mark price")?;
        let mut tiers = Vec::new();
        for tier in &market.leverage_tiers {
            if tier.max_leverage == 0 || tier.limit_order_risk_factor_bps > 10_000 {
                return Err("invalid leverage tier".into());
            }
            tiers.push(LeverageTier {
                upper_bound_size: base_lots(&tier.upper_bound_base_lots)?,
                max_leverage: Constant::new(tier.max_leverage),
                limit_order_risk_factor: BasisPoints::new(u64::from(
                    tier.limit_order_risk_factor_bps,
                )),
            });
        }
        let tiers = LeverageTiers::new(
            tiers
                .try_into()
                .map_err(|_| "four leverage tiers required")?,
        )?;
        if market
            .risk_factors_bps
            .iter()
            .chain([
                &market.cancel_order_risk_factor_bps,
                &market.upnl_risk_factor_bps,
                &market.upnl_withdrawal_risk_factor_bps,
            ])
            .any(|bps| *bps > 10_000)
        {
            return Err("risk factors exceed 10000 bps".into());
        }
        if let Some(index) = price_ticks(
            market.index_price_ticks.as_deref(),
            market.index_price_usd,
            &calculator,
        )? {
            indices.insert(market.symbol.clone(), index);
        }
        provider.insert(
            market.symbol.clone(),
            PerpAssetMetadata::new(
                market.symbol.clone(),
                market.asset_id,
                market.base_lot_decimals,
                mark,
                QuoteLotsPerBaseLotPerTick::new(tick_size),
                tiers,
                market.risk_factors_bps,
                market.cancel_order_risk_factor_bps,
                market.upnl_risk_factor_bps,
                market.upnl_withdrawal_risk_factor_bps,
            ),
        );
    }
    let mut portfolio = TraderPortfolio::builder()
        .authority(owner)
        .quote_lot_collateral(SignedQuoteLots::new(int(&request.collateral_quote_lots)?))
        .build();
    let mut funding = Vec::new();
    for row in &request.positions {
        if !provider.contains_key(&row.symbol) || portfolio.positions.contains_key(&row.symbol) {
            return Err("missing position metadata or duplicate position".into());
        }
        let base = int(&row.base_lots)?;
        let virtual_quote = int(&row.virtual_quote_lots)?;
        let sequence: u8 = row
            .sequence
            .parse()
            .map_err(|_| "invalid position sequence")?;
        // Same sign invariant used by the SDK's static liquidation calculation.
        if base != 0 && virtual_quote != 0 && base.signum() == virtual_quote.signum() {
            return Err("position base and virtual quote must have opposing signs".into());
        }
        let accumulated = SignedQuoteLotsI56::try_from(SignedQuoteLots::new(int(
            &row.accumulated_funding_quote_lots
        )?))
        .map_err(|_| "accumulated funding exceeds SDK bounds")?;
        portfolio.positions.insert(
            row.symbol.clone(),
            TraderPosition {
                base_lot_position: SignedBaseLots::new(base),
                virtual_quote_lot_position: SignedQuoteLots::new(virtual_quote),
                cumulative_funding_snapshot: SignedQuoteLotsPerBaseLot::ZERO,
                position_sequence_number: SequenceNumberU8::from(sequence),
                accumulated_funding_for_active_position: accumulated,
            },
        );
        // The API provides the already-computed signed payment, not a funding
        // rate or cumulative snapshot. Do not reverse engineer one by division.
        funding.push(MarginSimulationAction::ApplyFundingPayment {
            symbol: row.symbol.clone(),
            quote_lots: SignedQuoteLots::new(int(&row.unsettled_funding_quote_lots)?),
        });
    }
    let mut order_ids = HashSet::new();
    for row in &request.orders {
        let sequence = uint(&row.sequence)?;
        if !provider.contains_key(&row.symbol) || !order_ids.insert((row.symbol.clone(), sequence))
        {
            return Err("missing order metadata or duplicate order".into());
        }
        let side = match row.side.as_str() {
            "bid" => Side::Bid,
            "ask" => Side::Ask,
            _ => return Err("invalid order side".into()),
        };
        let remaining = base_lots(&row.remaining_base_lots)?;
        let initial = base_lots(&row.initial_base_lots)?;
        if remaining > initial {
            return Err("remaining order size exceeds initial size".into());
        }
        portfolio
            .limit_orders
            .entry(row.symbol.clone())
            .or_default()
            .push(LimitOrder {
                price: ticks(&row.price_ticks)?,
                side,
                order_sequence_number: sequence,
                base_lot_size: remaining,
                initial_trade_size: initial,
                reduce_only: row.reduce_only,
                is_stop_loss: row.is_stop_loss,
            });
    }
    let mut spot_params = HashMap::new();
    for params in &request.spot_params {
        if spot_params.insert(params.asset_index, params).is_some() {
            return Err("duplicate spot collateral params".into());
        }
    }
    let mut spot_ids = HashSet::new();
    for row in &request.spot_balances {
        if !spot_ids.insert(row.asset_index) {
            return Err("duplicate spot collateral balance".into());
        }
        let balance = uint(&row.balance)?;
        if balance == 0 {
            continue;
        }
        let params = spot_params
            .get(&row.asset_index)
            .ok_or("missing spot collateral params")?;
        if row.symbol != params.symbol
            || row.decimals != params.decimals
            || params.min_margin_discount_bps > params.max_margin_discount_bps
            || params.max_margin_discount_bps > 10_000
        {
            return Err("spot collateral configuration mismatch".into());
        }
        if !provider.contains_key(&params.pricing_market_symbol) {
            return Err("missing spot pricing market".into());
        }
        let index = *indices
            .get(&params.pricing_market_symbol)
            .ok_or("spot collateral requires a fresh index price, not mark")?;
        let cap = uint(&params.max_global_balance)?;
        if cap == 0 {
            return Err("spot global cap must be positive".into());
        }
        portfolio.spot_collaterals.push(SpotCollateralInput {
            asset_index: row.asset_index,
            symbol: row.symbol.clone(),
            pricing_market_symbol: params.pricing_market_symbol.clone(),
            balance,
            decimals: row.decimals,
            index_price: Some(index),
            max_global_balance: cap,
            min_margin_discount_bps: params.min_margin_discount_bps,
            max_margin_discount_bps: params.max_margin_discount_bps,
        });
    }
    let mut simulation = portfolio
        .simulate_margin(
            SimulateMarginParams {
                actions: funding,
                ..Default::default()
            },
            &provider,
        )
        .map_err(|e| format!("Rise risk computation failed: {e}"))?;
    let margin = simulation.after;
    // The SDK's portfolio views cast spot notionals to signed quote lots.
    // Reject out-of-range casts rather than accepting two's-complement values.
    margin
        .spot_collateral_notional()
        .checked_as_signed()
        .map_err(|_| "spot notional exceeds signed quote-lot bounds")?;
    margin
        .discounted_spot_collateral()
        .checked_as_signed()
        .map_err(|_| "discounted spot exceeds signed quote-lot bounds")?;
    simulation.liquidation_prices_usd.retain(|symbol, _| {
        portfolio
            .positions
            .get(symbol)
            .is_some_and(|position| !position.is_neutral())
    });
    let effective = margin.effective_collateral();
    let available = effective
        .checked_sub(
            margin
                .initial_margin()
                .checked_as_signed()
                .map_err(|_| "initial margin overflow")?,
        )
        .ok_or("available margin overflow")?
        .max(SignedQuoteLots::ZERO);
    if simulation
        .liquidation_prices_usd
        .values()
        .flatten()
        .any(|price| !price.is_finite() || *price < 0.0)
    {
        return Err("invalid SDK liquidation estimate".into());
    }
    Ok(RiskResult {
        owner_authority: request.owner_authority,
        trader_pda_index: 0,
        subaccount_index: 0,
        account_generation: request.account_generation,
        account_slot: request.account_slot,
        account_sequence: request.account_sequence,
        metadata_generation: request.metadata_generation,
        as_of_unix_ms: request.as_of_unix_ms,
        valid_until_unix_ms: valid_until,
        quote_decimals: 6,
        equity_quote_lots: margin.portfolio_value().as_inner().to_string(),
        effective_collateral_quote_lots: effective.as_inner().to_string(),
        available_margin_quote_lots: available.as_inner().to_string(),
        initial_margin_quote_lots: margin.margin.initial_margin.as_inner().to_string(),
        maintenance_margin_quote_lots: margin.margin.maintenance_margin.as_inner().to_string(),
        unsettled_funding_quote_lots: margin.margin.unsettled_funding.as_inner().to_string(),
        unrealized_pnl_quote_lots: margin.margin.unrealized_pnl.as_inner().to_string(),
        spot_notional_quote_lots: margin.spot_collateral_notional().as_inner().to_string(),
        discounted_spot_quote_lots: margin.discounted_spot_collateral().as_inner().to_string(),
        risk_tier: format!("{:?}", margin.risk_tier().map_err(|e| e.to_string())?),
        liquidation_model: "rise_local_static",
        static_liquidation_prices_usd: simulation.liquidation_prices_usd.into_iter().collect(),
    })
}

fn expiry(now: u64, observed: u64, age: u64) -> Result<u64, String> {
    if observed == 0 || observed > now || now - observed > age {
        return Err("risk inputs missing, future-dated or stale".into());
    }
    observed
        .checked_add(age)
        .ok_or_else(|| "risk timestamp overflow".into())
}
fn int(value: &str) -> Result<i64, String> {
    value
        .parse()
        .map_err(|_| "invalid signed quote/base lots".into())
}
fn uint(value: &str) -> Result<u64, String> {
    value.parse().map_err(|_| "invalid unsigned integer".into())
}
fn base_lots(value: &str) -> Result<BaseLots, String> {
    let value = uint(value)?;
    if value > u64::from(u32::MAX) {
        return Err("base lots exceed SDK bounds".into());
    }
    Ok(BaseLots::new(value))
}
fn ticks(value: &str) -> Result<Ticks, String> {
    let value = uint(value)?;
    if value == 0 || value > u64::from(u32::MAX) {
        return Err("price ticks outside SDK bounds".into());
    }
    Ok(Ticks::new(value))
}

fn price_ticks(
    raw: Option<&str>,
    usd: Option<f64>,
    calculator: &MarketCalculator,
) -> Result<Option<Ticks>, String> {
    match (raw, usd) {
        (Some(value), None) => ticks(value).map(Some),
        (None, Some(value)) => {
            if !value.is_finite() || value <= 0.0 {
                return Err("invalid USD risk price".into());
            }
            let value = calculator
                .price_to_ticks(value)
                .map_err(|_| "USD price conversion failed")?;
            if value == Ticks::ZERO || value.as_inner() > u64::from(u32::MAX) {
                return Err("converted price outside SDK bounds".into());
            }
            Ok(Some(value))
        }
        (None, None) => Ok(None),
        _ => Err("supply ticks or USD, not both".into()),
    }
}
