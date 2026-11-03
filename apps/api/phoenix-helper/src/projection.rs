//! Full-fill planning projection, not transaction simulation or authorization.
use std::collections::HashMap;

use phoenix_rise::math::portfolio::{
    MarginSimulationAction, SimulateMarginParams, TraderPortfolioMargin,
};
use phoenix_rise::math::{
    BasisPoints, FeeRateMicro, PerpAssetMetadata, QuoteLots, Side, SignedQuoteLots, TraderPortfolio,
};
use serde::{Deserialize, Serialize};

use crate::risk::{base_lots, ticks};

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OrderProjection {
    pub intent_id: String,
    pub symbol: String,
    pub side: String,
    pub num_base_lots: String,
    pub price_limit_ticks: String,
    pub quote_observed_unix_ms: u64,
    pub taker_fee_multiplier: f64,
    pub flight_fee_bps: u16,
    pub reduce_only: bool,
}

#[derive(Serialize)]
pub struct ProjectionResult {
    intent_id: String,
    symbol: String,
    side: String,
    num_base_lots: String,
    price_limit_ticks: String,
    flight_fee_bps: u16,
    model: &'static str,
    fee_policy: &'static str,
    notional_quote_lots: String,
    exchange_fee_quote_lots: String,
    flight_fee_quote_lots: String,
    total_fee_quote_lots: String,
    projected_base_lots: String,
    projected_equity_quote_lots: String,
    projected_effective_collateral_quote_lots: String,
    projected_initial_margin_quote_lots: String,
    projected_maintenance_margin_quote_lots: String,
    projected_available_margin_quote_lots: String,
    projected_risk_tier: String,
    margin_sufficient: bool,
    reason: &'static str,
}

pub fn project(
    order: &OrderProjection,
    portfolio: &TraderPortfolio,
    provider: &HashMap<String, PerpAssetMetadata>,
    funding: &[MarginSimulationAction],
    before: &TraderPortfolioMargin,
    taker_fee: f64,
    isolated_only: bool,
) -> Result<ProjectionResult, String> {
    if order.intent_id.is_empty()
        || order.intent_id.len() > 128
        || order.symbol.is_empty()
        || order.reduce_only
        || order.flight_fee_bps != 2
        || isolated_only
    {
        return Err("invalid cross open projection intent or Flight fee".into());
    }
    if !order.taker_fee_multiplier.is_finite()
        || order.taker_fee_multiplier < 0.0
        || !taker_fee.is_finite()
        || taker_fee < 0.0
    {
        return Err("invalid projected taker fee".into());
    }
    let side = match order.side.as_str() {
        "bid" => Side::Bid,
        "ask" => Side::Ask,
        _ => return Err("invalid projected order side".into()),
    };
    let size = base_lots(&order.num_base_lots)?;
    if size.as_inner() == 0 {
        return Err("projected order size must be positive".into());
    }
    let price = ticks(&order.price_limit_ticks)?;
    let market = provider
        .get(&order.symbol)
        .ok_or("missing projected market metadata")?;
    let notional = size * (price * market.tick_size());
    if notional == QuoteLots::ZERO {
        return Err("projected notional must be positive".into());
    }
    // Use an upper-bound rate, no unverified referral/tier discount. Each venue
    // fee component is rounded up separately through the SDK fee primitive.
    let micro = (taker_fee * order.taker_fee_multiplier.max(1.0) * 1_000_000.0).ceil();
    if !(0.0..=1_000_000.0).contains(&micro) {
        return Err("projected fee exceeds SDK bounds".into());
    }
    let exchange = FeeRateMicro::new_checked(micro as u32)
        .map_err(|_| "invalid exchange fee rate")?
        .apply_to_quote_lots(notional)
        .ok_or("exchange fee overflow")?;
    let flight = FeeRateMicro::new_checked(u32::from(order.flight_fee_bps) * 100)
        .map_err(|_| "invalid Flight fee rate")?
        .apply_to_quote_lots(notional)
        .ok_or("Flight fee overflow")?;
    let total_fee = exchange
        .checked_add(flight)
        .ok_or("projected total fee overflow")?;
    let mut actions = funding.to_vec();
    actions.push(MarginSimulationAction::FillPosition {
        symbol: order.symbol.clone(),
        side,
        base_lots: size,
        price_ticks: price,
        fee_quote_lots: total_fee,
    });
    let simulation = portfolio
        .simulate_margin(
            SimulateMarginParams {
                actions,
                ..Default::default()
            },
            provider,
        )
        .map_err(|e| format!("Rise order projection failed: {e}"))?;
    let after = &simulation.after;
    before
        .spot_collateral_notional()
        .checked_as_signed()
        .map_err(|_| "spot notional overflow")?;
    after
        .spot_collateral_notional()
        .checked_as_signed()
        .map_err(|_| "projected spot notional overflow")?;
    after
        .discounted_spot_collateral()
        .checked_as_signed()
        .map_err(|_| "projected spot collateral overflow")?;
    let before_available = headroom(before)?;
    let after_available = headroom(after)?;
    let consumed = before_available.saturating_sub(after_available);
    let allowed = BasisPoints::new(9000)
        .apply_to_quote_lots(QuoteLots::new(before_available))
        .ok_or("margin buffer overflow")?;
    let before_tier = format!("{:?}", before.risk_tier().map_err(|e| e.to_string())?);
    let after_tier = format!("{:?}", after.risk_tier().map_err(|e| e.to_string())?);
    let reason = if before_tier != "Safe" {
        "current_account_not_safe"
    } else if after_tier != "Safe" {
        "projected_account_not_safe"
    } else if consumed > allowed.as_inner() {
        "insufficient_margin_with_buffer"
    } else {
        ""
    };
    let projected_base = simulation
        .projected_portfolio
        .positions
        .get(&order.symbol)
        .map(|p| p.base_lot_position.as_inner())
        .unwrap_or(0);
    Ok(ProjectionResult {
        intent_id: order.intent_id.clone(),
        symbol: order.symbol.clone(),
        side: order.side.clone(),
        num_base_lots: order.num_base_lots.clone(),
        price_limit_ticks: order.price_limit_ticks.clone(),
        flight_fee_bps: 2,
        model: "rise_full_fill_at_limit",
        fee_policy: "undiscounted_taker_ceiling",
        notional_quote_lots: notional.as_inner().to_string(),
        exchange_fee_quote_lots: exchange.as_inner().to_string(),
        flight_fee_quote_lots: flight.as_inner().to_string(),
        total_fee_quote_lots: total_fee.as_inner().to_string(),
        projected_base_lots: projected_base.to_string(),
        projected_equity_quote_lots: after.portfolio_value().as_inner().to_string(),
        projected_effective_collateral_quote_lots: after
            .effective_collateral()
            .as_inner()
            .to_string(),
        projected_initial_margin_quote_lots: after.margin.initial_margin.as_inner().to_string(),
        projected_maintenance_margin_quote_lots: after
            .margin
            .maintenance_margin
            .as_inner()
            .to_string(),
        projected_available_margin_quote_lots: after_available.to_string(),
        projected_risk_tier: after_tier,
        margin_sufficient: reason.is_empty(),
        reason,
    })
}

fn headroom(margin: &TraderPortfolioMargin) -> Result<u64, String> {
    let available = margin
        .effective_collateral()
        .checked_sub(
            margin
                .initial_margin()
                .checked_as_signed()
                .map_err(|_| "margin overflow")?,
        )
        .ok_or("headroom overflow")?
        .max(SignedQuoteLots::ZERO);
    Ok(available.as_inner() as u64)
}
