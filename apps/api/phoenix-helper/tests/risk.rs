use std::io::Write;
use std::process::{Command, Stdio};

use serde_json::{Value, json};

fn fixture() -> Value {
    serde_json::from_str(include_str!(
        "../../internal/venue/phoenix/helper/testdata/risk.json"
    ))
    .unwrap()
}

fn compute(payload: Value) -> Value {
    let mut child = Command::new(env!("CARGO_BIN_EXE_phoenix-helper"))
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let frame = json!({"protocol_version":1,"request_id":"risk-1","operation":"compute_risk","payload":payload});
    child
        .stdin
        .take()
        .unwrap()
        .write_all(&serde_json::to_vec(&frame).unwrap())
        .unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(
        output.status.success(),
        "helper crashed instead of returning a result"
    );
    serde_json::from_slice(&output.stdout).unwrap()
}

#[test]
fn quote_collateral_and_signed_pending_funding_use_sdk_units_once() {
    let response = compute(fixture());
    assert_eq!(response["ok"], true, "{response}");
    let r = &response["result"];
    assert_eq!(r["quote_decimals"], 6);
    assert_eq!(r["equity_quote_lots"], "108000000");
    assert_eq!(r["effective_collateral_quote_lots"], "103000000");
    assert_eq!(r["available_margin_quote_lots"], "92000000");
    assert_eq!(r["initial_margin_quote_lots"], "11000000");
    assert_eq!(r["maintenance_margin_quote_lots"], "5500000");
    assert_eq!(r["unsettled_funding_quote_lots"], "-2000000");
    assert_eq!(r["unrealized_pnl_quote_lots"], "10000000");
    assert_eq!(r["risk_tier"], "Safe");
    assert_eq!(r["valid_until_unix_ms"], 1029000);
    assert_eq!(r["liquidation_model"], "rise_local_static");
    let long = r["static_liquidation_prices_usd"]["SOL"].as_f64().unwrap();
    assert!(long > 0.0 && long < 110.0);

    let mut short = fixture();
    short["positions"][0]["base_lots"] = json!("-1000");
    short["positions"][0]["virtual_quote_lots"] = json!("100000000");
    let short = compute(short);
    assert_eq!(short["ok"], true, "{short}");
    assert_eq!(short["result"]["equity_quote_lots"], "88000000");
    assert!(
        short["result"]["static_liquidation_prices_usd"]["SOL"]
            .as_f64()
            .unwrap()
            > 110.0
    );

    let mut receive = fixture();
    receive["positions"][0]["unsettled_funding_quote_lots"] = json!("2000000");
    let receive = compute(receive);
    assert_eq!(receive["result"]["equity_quote_lots"], "112000000");
}

#[test]
fn sol_collateral_uses_index_haircut_without_sol_perp_exposure() {
    let mut request = fixture();
    request["positions"] = json!([]);
    request["spot_balances"] =
        json!([{"asset_index":4294901760u32,"symbol":"SOL","balance":"1000000000","decimals":9}]);
    request["spot_params"] = json!([{"asset_index":4294901760u32,"symbol":"SOL","pricing_market_symbol":"SOL","decimals":9,"max_global_balance":"1000000000000","min_margin_discount_bps":1000,"max_margin_discount_bps":2000}]);
    let response = compute(request.clone());
    assert_eq!(response["ok"], true, "{response}");
    let r = &response["result"];
    assert_eq!(r["spot_notional_quote_lots"], "100000000"); // index $100, not mark $110
    assert_eq!(r["discounted_spot_quote_lots"], "89990000");
    assert_eq!(r["equity_quote_lots"], "200000000");
    assert_eq!(r["available_margin_quote_lots"], "189990000");
    assert_eq!(r["static_liquidation_prices_usd"], json!({}));
    request["markets"][0]["index_price_ticks"] = Value::Null;
    assert_eq!(
        compute(request)["ok"],
        false,
        "missing index must not fall back to mark"
    );
}

#[test]
fn resting_orders_consume_margin_and_reduce_only_does_not_add_exposure() {
    let mut request = fixture();
    let order = json!({"symbol":"SOL","sequence":"42","side":"bid","price_ticks":"11000","remaining_base_lots":"1000","initial_base_lots":"1000","reduce_only":false,"is_stop_loss":false});
    request["orders"] = json!([order]);
    let increasing = compute(request.clone());
    assert_eq!(increasing["ok"], true, "{increasing}");
    let initial: u64 = increasing["result"]["initial_margin_quote_lots"]
        .as_str()
        .unwrap()
        .parse()
        .unwrap();
    assert!(initial > 11000000);
    request["orders"][0]["side"] = json!("ask");
    request["orders"][0]["reduce_only"] = json!(true);
    let reducing = compute(request);
    assert_eq!(reducing["ok"], true, "{reducing}");
    assert_eq!(reducing["result"]["initial_margin_quote_lots"], "11000000");
}

#[test]
fn incomplete_stale_or_invalid_inputs_never_return_risk_values() {
    for (path, value) in [
        ("/trader_pda_index", json!(1)),
        ("/has_splines", json!(true)),
        ("/account_observed_unix_ms", json!(969999)),
        ("/params_observed_unix_ms", json!(699999)),
        ("/markets/0/price_observed_unix_ms", json!(969999)),
        ("/markets/0/price_observed_unix_ms", json!(1000001)),
        ("/markets/0/mark_price_ticks", json!("0")),
        ("/markets/0/risk_factors_bps/0", json!(10001)),
        (
            "/markets/0/leverage_tiers/1/upper_bound_base_lots",
            json!("1"),
        ),
        ("/positions/0/base_lots", json!("NaN")),
        ("/collateral_quote_lots", json!("1.5")),
        ("/markets", json!([])),
    ] {
        let mut request = fixture();
        *request.pointer_mut(path).unwrap() = value;
        let response = compute(request);
        assert_eq!(response["ok"], false, "accepted {path}: {response}");
        assert_eq!(response["error"]["code"], "invalid_risk_request");
        assert!(response.get("result").is_none());
    }
    let mut request = fixture();
    let duplicate = request["positions"][0].clone();
    request["positions"].as_array_mut().unwrap().push(duplicate);
    assert_eq!(compute(request)["ok"], false);
}

#[test]
fn overflowing_sdk_totals_do_not_become_positive_margin() {
    let mut request = fixture();
    request["collateral_quote_lots"] = json!("-9223372036854775808");
    request["positions"][0]["virtual_quote_lots"] = json!("-9223372036854775807");
    request["positions"][0]["unsettled_funding_quote_lots"] = json!("0");
    let response = compute(request);
    assert_eq!(
        response["ok"], false,
        "overflow returned spendable margin: {response}"
    );
}

#[test]
fn cross_margin_aggregates_markets_and_honors_leverage_tiers() {
    let mut request = fixture();
    let mut market = request["markets"][0].clone();
    market["symbol"] = json!("ETH");
    market["asset_id"] = json!(2);
    market["mark_price_ticks"] = json!("10000");
    request["markets"].as_array_mut().unwrap().push(market);
    let mut position = request["positions"][0].clone();
    position["symbol"] = json!("ETH");
    position["unsettled_funding_quote_lots"] = json!("0");
    request["positions"].as_array_mut().unwrap().push(position);
    let response = compute(request);
    assert_eq!(response["ok"], true, "{response}");
    assert_eq!(response["result"]["equity_quote_lots"], "108000000");
    assert_eq!(response["result"]["initial_margin_quote_lots"], "21000000");
    assert_eq!(
        response["result"]["available_margin_quote_lots"],
        "82000000"
    );
    assert_eq!(
        response["result"]["static_liquidation_prices_usd"]
            .as_object()
            .unwrap()
            .len(),
        2
    );

    let mut request = fixture();
    request["markets"][0]["leverage_tiers"][0]["max_leverage"] = json!(5);
    let response = compute(request);
    assert_eq!(response["result"]["initial_margin_quote_lots"], "22000000");
    assert_eq!(
        response["result"]["available_margin_quote_lots"],
        "81000000"
    );
}

#[test]
fn underwater_and_flat_pending_funding_are_not_spendable_margin() {
    let mut request = fixture();
    request["collateral_quote_lots"] = json!("0");
    request["markets"][0]["mark_price_ticks"] = json!("5000");
    let response = compute(request);
    assert_eq!(response["ok"], true, "{response}");
    assert_eq!(response["result"]["equity_quote_lots"], "-52000000");
    assert_eq!(response["result"]["available_margin_quote_lots"], "0");
    assert_eq!(response["result"]["risk_tier"], "HighRisk");

    let mut request = fixture();
    request["positions"][0]["base_lots"] = json!("0");
    request["positions"][0]["virtual_quote_lots"] = json!("0");
    let response = compute(request);
    assert_eq!(response["ok"], true, "{response}");
    assert_eq!(response["result"]["equity_quote_lots"], "98000000");
    assert_eq!(
        response["result"]["static_liquidation_prices_usd"],
        json!({})
    );
}

#[test]
fn usd_prices_use_sdk_tick_conversion_and_reject_ambiguous_units() {
    let mut request = fixture();
    request["markets"][0]
        .as_object_mut()
        .unwrap()
        .remove("mark_price_ticks");
    request["markets"][0]
        .as_object_mut()
        .unwrap()
        .remove("index_price_ticks");
    request["markets"][0]["mark_price_usd"] = json!(110.0);
    request["markets"][0]["index_price_usd"] = json!(100.0);
    let response = compute(request.clone());
    assert_eq!(response["ok"], true, "{response}");
    assert_eq!(response["result"]["equity_quote_lots"], "108000000");
    assert_eq!(response["result"]["initial_margin_quote_lots"], "11000000");
    request["markets"][0]["mark_price_ticks"] = json!("11000");
    assert_eq!(compute(request)["ok"], false);
}
