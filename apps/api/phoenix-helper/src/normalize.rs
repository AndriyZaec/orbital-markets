use phoenix_rise::math::{BaseLots, MarketCalculator, QuoteLotsPerBaseLotPerTick, Ticks};
use rust_decimal::Decimal;
use rust_decimal::prelude::ToPrimitive;
use serde::{Deserialize, Serialize};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct NormalizeRequest {
    symbol: String,
    side: String,
    amount: String,
    limit_price: String,
    base_lot_decimals: i8,
    tick_size: String,
    metadata_generation: u64,
}

#[derive(Serialize)]
pub struct NormalizedOrder {
    symbol: String,
    side: String,
    amount: String,
    limit_price: String,
    num_base_lots: u64,
    price_limit_ticks: u64,
    base_lot_decimals: i8,
    tick_size: String,
    metadata_generation: u64,
}

pub fn normalize(q: NormalizeRequest) -> Result<NormalizedOrder, String> {
    if q.symbol.is_empty()
        || q.symbol.len() > 32
        || q.metadata_generation == 0
        || !(-18..=18).contains(&q.base_lot_decimals)
    {
        return Err("invalid order normalization context".into());
    }
    let tick_size: u64 = q.tick_size.parse().map_err(|_| "invalid tick size")?;
    if tick_size == 0 {
        return Err("tick size must be positive".into());
    }
    let amount = Decimal::from_str_exact(&q.amount).map_err(|_| "invalid decimal amount")?;
    let limit =
        Decimal::from_str_exact(&q.limit_price).map_err(|_| "invalid decimal price limit")?;
    if amount <= Decimal::ZERO || limit <= Decimal::ZERO {
        return Err("amount and price must be positive".into());
    }
    let calculator = MarketCalculator::new(
        q.base_lot_decimals,
        QuoteLotsPerBaseLotPerTick::new(tick_size),
    );
    // SDK defines the units. Only directional decimal quantization lives here;
    // f64 floor would incorrectly turn e.g. 0.29 / 0.01 into 28 lots.
    let lot_unit = calculator.base_lots_to_decimal(BaseLots::new(1));
    let tick_unit = calculator.ticks_to_decimal(Ticks::new(1));
    let lots = amount
        .checked_div(lot_unit)
        .ok_or("base amount conversion overflow")?
        .floor()
        .to_u64()
        .ok_or("base lots overflow")?;
    let ratio = limit
        .checked_div(tick_unit)
        .ok_or("price conversion overflow")?;
    let ticks = match q.side.as_str() {
        "bid" => ratio.floor(),
        "ask" => ratio.ceil(),
        _ => return Err("invalid order side".into()),
    }
    .to_u64()
    .ok_or("price ticks overflow")?;
    if lots == 0 || lots > u64::from(u32::MAX) || ticks == 0 || ticks > u64::from(u32::MAX) {
        return Err("normalized order is zero or outside SDK bounds".into());
    }
    let normalized_amount = calculator.base_lots_to_decimal(BaseLots::new(lots));
    let normalized_limit = calculator.ticks_to_decimal(Ticks::new(ticks));
    if normalized_amount > amount
        || (q.side == "bid" && normalized_limit > limit)
        || (q.side == "ask" && normalized_limit < limit)
    {
        return Err("normalization widened approved order bounds".into());
    }
    Ok(NormalizedOrder {
        symbol: q.symbol,
        side: q.side,
        amount: normalized_amount.normalize().to_string(),
        limit_price: normalized_limit.normalize().to_string(),
        num_base_lots: lots,
        price_limit_ticks: ticks,
        base_lot_decimals: q.base_lot_decimals,
        tick_size: q.tick_size,
        metadata_generation: q.metadata_generation,
    })
}
