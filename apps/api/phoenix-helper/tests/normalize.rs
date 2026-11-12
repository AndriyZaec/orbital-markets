use serde_json::{Value, json};
use std::io::Write;
use std::process::{Command, Stdio};

fn normalize(amount: &str, limit: &str, side: &str, decimals: i8, tick: &str) -> Value {
    let mut child = Command::new(env!("CARGO_BIN_EXE_phoenix-helper"))
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let q = json!({"protocol_version":1,"request_id":"normalize-1","operation":"normalize_order","payload":{"symbol":"SOL","side":side,"amount":amount,"limit_price":limit,"base_lot_decimals":decimals,"tick_size":tick,"metadata_generation":7}});
    child
        .stdin
        .take()
        .unwrap()
        .write_all(&serde_json::to_vec(&q).unwrap())
        .unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(output.status.success());
    serde_json::from_slice(&output.stdout).unwrap()
}

#[test]
fn decimal_amount_and_limits_round_inward_without_binary_float_lot_loss() {
    let bid = normalize("0.29", "100.005", "bid", 2, "100");
    assert_eq!(bid["ok"], true, "{bid}");
    assert_eq!(bid["result"]["num_base_lots"], 29);
    assert_eq!(bid["result"]["amount"], "0.29");
    assert_eq!(bid["result"]["price_limit_ticks"], 10000);
    assert_eq!(bid["result"]["limit_price"], "100");
    let ask = normalize("0.299", "100.005", "ask", 2, "100");
    assert_eq!(ask["ok"], true, "{ask}");
    assert_eq!(ask["result"]["num_base_lots"], 29);
    assert_eq!(ask["result"]["price_limit_ticks"], 10001);
    assert_eq!(ask["result"]["limit_price"], "100.01");
}

#[test]
fn negative_base_decimals_follow_sdk_units() {
    let bid = normalize("150", "0.00000010", "bid", -2, "3");
    assert_eq!(bid["ok"], true, "{bid}");
    assert_eq!(bid["result"]["num_base_lots"], 1);
    assert_eq!(bid["result"]["amount"], "100");
    assert_eq!(bid["result"]["price_limit_ticks"], 3);
    let ask = normalize("150", "0.00000010", "ask", -2, "3");
    assert_eq!(ask["result"]["price_limit_ticks"], 4);
}

#[test]
fn zero_sub_lot_nonfinite_and_out_of_range_requests_return_no_order() {
    for (amount, price, side, dec, tick) in [
        ("0", "100", "bid", 2, "100"),
        ("0.001", "100", "bid", 2, "100"),
        ("1", "0.001", "bid", 2, "100"),
        ("NaN", "100", "bid", 2, "100"),
        ("1", "Infinity", "ask", 2, "100"),
        ("4294967296", "100", "bid", 0, "100"),
        ("1", "1000000000000", "bid", 2, "100"),
        ("1", "100", "bad", 2, "100"),
        ("1", "100", "bid", 2, "0"),
    ] {
        let response = normalize(amount, price, side, dec, tick);
        assert_eq!(response["ok"], false, "{response}");
        assert!(response.get("result").is_none());
    }
}
