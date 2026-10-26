use std::io::Write;
use std::process::{Command, Stdio};

const BUILD_ORDER_REQUEST: &[u8] = br#"{"protocol_version":1,"request_id":"order-1","operation":"build_order","payload":{"action":"open","owner_authority":"11111111111111111111111111111111","position_authority":"So11111111111111111111111111111111111111112","fee_payer":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v","builder_authority":"Vote111111111111111111111111111111111111111","symbol":"SOL","side":"bid","num_base_lots":100,"price_limit_ticks":12345,"reduce_only":false,"client_order_id":42,"last_valid_slot":123456,"recent_blockhash":"11111111111111111111111111111111","last_valid_block_height":999999,"metadata_generation":7,"accounts":{"perp_asset_map":"PhUsd11YkbjSaWjFncfAAmatntsjx3MgDR9B6g1ks3A","orderbook":"EMBERpYNE6ehWmXymZZS2skiFmCa9V5dp14e1iduM5qy","spline_collection":"RiSeVw3ZjNfsaXPRb4mgaqYaEEt41pNNJoDvVh7pgQj","global_trader_index":["F1ightu9cujFYo34k9CabifLrJT8qzfDVM2Q7BqhJn2W"],"active_trader_buffer":["phDEVv4w6BcfkLrLNeXr8HhhgQxnxziVGXpGPcaadMf"]}}}
"#;

fn run_helper(input: &[u8]) -> std::process::Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_phoenix-helper"))
        .env_remove("PHOENIX_ENV")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .expect("helper should start");

    child
        .stdin
        .take()
        .expect("stdin should be piped")
        .write_all(input)
        .expect("request should be written");

    child.wait_with_output().expect("helper should exit")
}

fn safety_order_request(action: &str) -> String {
    String::from_utf8(BUILD_ORDER_REQUEST.to_vec())
        .expect("fixture should be utf-8")
        .replace("\"action\":\"open\"", &format!("\"action\":\"{action}\""))
        .replace("\"reduce_only\":false", "\"reduce_only\":true")
        .replace(
            ",\"builder_authority\":\"Vote111111111111111111111111111111111111111\"",
            "",
        )
}

#[test]
fn health_reports_protocol_and_dependency_versions() {
    let output = run_helper(
        br#"{"protocol_version":1,"request_id":"health-1","operation":"health"}
"#,
    );

    assert!(output.status.success());
    assert_eq!(
        String::from_utf8(output.stdout).expect("response should be utf-8"),
        concat!(
            "{\"protocol_version\":1,\"request_id\":\"health-1\",",
            "\"ok\":true,\"result\":{\"helper_version\":\"0.1.0\",",
            "\"phoenix_rise_version\":\"0.6.13\"}}\n"
        )
    );
}

#[test]
fn unsupported_protocol_version_is_a_request_error() {
    let output = run_helper(
        br#"{"protocol_version":2,"request_id":"version-1","operation":"health"}
"#,
    );

    assert!(output.status.success());
    assert_eq!(
        String::from_utf8(output.stdout).expect("response should be utf-8"),
        concat!(
            "{\"protocol_version\":1,\"request_id\":\"version-1\",",
            "\"ok\":false,\"error\":{\"code\":\"unsupported_protocol_version\",",
            "\"message\":\"expected protocol version 1\"}}\n"
        )
    );
}

#[test]
fn unknown_operation_is_a_request_error() {
    let output = run_helper(
        br#"{"protocol_version":1,"request_id":"operation-1","operation":"cancel_order"}
"#,
    );

    assert!(output.status.success());
    assert_eq!(
        String::from_utf8(output.stdout).expect("response should be utf-8"),
        concat!(
            "{\"protocol_version\":1,\"request_id\":\"operation-1\",",
            "\"ok\":false,\"error\":{\"code\":\"unsupported_operation\",",
            "\"message\":\"unsupported operation: cancel_order\"}}\n"
        )
    );
}

#[test]
fn build_order_returns_a_deterministic_cross_margin_message() {
    let first = run_helper(BUILD_ORDER_REQUEST);
    let second = run_helper(BUILD_ORDER_REQUEST);

    assert!(first.status.success());
    assert_eq!(first.stdout, second.stdout);

    let response: serde_json::Value =
        serde_json::from_slice(&first.stdout).expect("response should be json");
    assert_eq!(response["ok"], true);
    assert_eq!(response["request_id"], "order-1");
    assert_eq!(response["result"]["trader_pda_index"], 0);
    assert_eq!(response["result"]["subaccount_index"], 0);
    assert_eq!(response["result"]["last_valid_block_height"], 999999);
    assert_eq!(response["result"]["metadata_generation"], 7);
    assert_eq!(
        response["result"]["trader_account"],
        "8f1SJ8oVHyhTvxSYTCsGszbwLW5QYYGLCmvtTcn2qPEZ"
    );
    assert_eq!(response["result"]["trader_bump"], 255);
    assert_eq!(
        response["result"]["message_sha256"],
        "0a009f7f2a2d4be1984d44a9574b81182bf73874313f3f685873eeb64e0ca7dc"
    );
    assert!(
        response["result"]["message_base64"]
            .as_str()
            .is_some_and(|message| message.len() > 400)
    );
    assert_eq!(
        response["result"]["required_signers"],
        serde_json::json!([
            {
                "index": 0,
                "pubkey": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
                "role": "fee_payer"
            },
            {
                "index": 1,
                "pubkey": "So11111111111111111111111111111111111111112",
                "role": "position_authority"
            }
        ])
    );
    assert_eq!(response["result"]["manifest"]["intent_action"], "open");
    assert_eq!(response["result"]["manifest"]["routing"], "flight");
    assert_eq!(
        response["result"]["manifest"]["flight"]["builder_authority"],
        "Vote111111111111111111111111111111111111111"
    );
    assert_eq!(
        response["result"]["manifest"]["flight"]["flight_program_id"],
        "F1ightu9cujFYo34k9CabifLrJT8qzfDVM2Q7BqhJn2W"
    );
    assert_eq!(
        response["result"]["manifest"]["flight"]["builder_pda_index"],
        0
    );
    assert_eq!(
        response["result"]["manifest"]["flight"]["builder_subaccount_index"],
        0
    );
    assert_eq!(
        response["result"]["manifest"]["flight"]["expected_registered_fee_bps"],
        2
    );
    assert_eq!(
        response["result"]["manifest"]["flight"]["uses_fee_override"],
        false
    );
    assert_eq!(response["result"]["manifest"]["requested_symbol"], "SOL");
    assert_eq!(
        response["result"]["manifest"]["orderbook"],
        "EMBERpYNE6ehWmXymZZS2skiFmCa9V5dp14e1iduM5qy"
    );
    assert_eq!(response["result"]["manifest"]["reduce_only"], false);
    assert_eq!(response["result"]["manifest"]["num_base_lots"], 100);
    assert_eq!(response["result"]["manifest"]["price_limit_ticks"], 12345);
    assert_eq!(
        response["result"]["manifest"]["phoenix_program_id"],
        "EtrnLzgbS7nMMy5fbD42kXiUzGg8XQzJ972Xtk1cjWih"
    );
    assert_eq!(
        response["result"]["manifest"]["instructions"][0]["program_id"],
        "F1ightu9cujFYo34k9CabifLrJT8qzfDVM2Q7BqhJn2W"
    );
}

#[test]
fn unwind_order_bypasses_flight() {
    let request = safety_order_request("unwind");
    let output = run_helper(request.as_bytes());

    assert!(output.status.success());
    let response: serde_json::Value =
        serde_json::from_slice(&output.stdout).expect("response should be json");
    assert_eq!(response["ok"], true);
    assert_eq!(response["result"]["manifest"]["intent_action"], "unwind");
    assert_eq!(response["result"]["manifest"]["routing"], "native");
    assert!(response["result"]["manifest"]["flight"].is_null());
    assert_eq!(
        response["result"]["manifest"]["instructions"][0]["program_id"],
        "EtrnLzgbS7nMMy5fbD42kXiUzGg8XQzJ972Xtk1cjWih"
    );
}

#[test]
fn emergency_close_bypasses_flight() {
    let request = safety_order_request("emergency_close");
    let output = run_helper(request.as_bytes());

    assert!(output.status.success());
    let response: serde_json::Value =
        serde_json::from_slice(&output.stdout).expect("response should be json");
    assert_eq!(response["ok"], true);
    assert_eq!(
        response["result"]["manifest"]["intent_action"],
        "emergency_close"
    );
    assert_eq!(response["result"]["manifest"]["routing"], "native");
    assert!(response["result"]["manifest"]["flight"].is_null());
    assert_eq!(
        response["result"]["manifest"]["instructions"][0]["program_id"],
        "EtrnLzgbS7nMMy5fbD42kXiUzGg8XQzJ972Xtk1cjWih"
    );
}

#[test]
fn normal_close_uses_flight() {
    let request = String::from_utf8(BUILD_ORDER_REQUEST.to_vec())
        .expect("fixture should be utf-8")
        .replace("\"action\":\"open\"", "\"action\":\"close\"")
        .replace("\"reduce_only\":false", "\"reduce_only\":true");
    let output = run_helper(request.as_bytes());

    assert!(output.status.success());
    let response: serde_json::Value =
        serde_json::from_slice(&output.stdout).expect("response should be json");
    assert_eq!(response["ok"], true);
    assert_eq!(response["result"]["manifest"]["intent_action"], "close");
    assert_eq!(response["result"]["manifest"]["routing"], "flight");
    assert_eq!(
        response["result"]["manifest"]["instructions"][0]["program_id"],
        "F1ightu9cujFYo34k9CabifLrJT8qzfDVM2Q7BqhJn2W"
    );
}

#[test]
fn build_order_rejects_open_reduce_only_intent() {
    let request = String::from_utf8(BUILD_ORDER_REQUEST.to_vec())
        .expect("fixture should be utf-8")
        .replace("\"reduce_only\":false", "\"reduce_only\":true");
    let output = run_helper(request.as_bytes());

    assert!(output.status.success());
    let response: serde_json::Value =
        serde_json::from_slice(&output.stdout).expect("response should be json");
    assert_eq!(response["ok"], false);
    assert_eq!(response["error"]["code"], "invalid_order_request");
    assert_eq!(
        response["error"]["message"],
        "open must not be reduce-only; safety actions must be reduce-only"
    );
}

#[test]
fn build_order_rejects_owner_as_position_authority() {
    let request = String::from_utf8(BUILD_ORDER_REQUEST.to_vec())
        .expect("fixture should be utf-8")
        .replace(
            "\"owner_authority\":\"11111111111111111111111111111111\"",
            "\"owner_authority\":\"So11111111111111111111111111111111111111112\"",
        );
    let output = run_helper(request.as_bytes());

    assert!(output.status.success());
    let response: serde_json::Value =
        serde_json::from_slice(&output.stdout).expect("response should be json");
    assert_eq!(response["ok"], false);
    assert_eq!(response["error"]["code"], "invalid_order_request");
    assert_eq!(
        response["error"]["message"],
        "position_authority must differ from owner_authority"
    );
}

#[test]
fn malformed_frame_does_not_stop_following_requests() {
    let output = run_helper(
        br#"not-json
{"protocol_version":1,"request_id":"health-after-error","operation":"health"}
"#,
    );

    assert!(output.status.success());
    let responses = String::from_utf8(output.stdout).expect("responses should be utf-8");
    let mut lines = responses.lines();
    let error: serde_json::Value =
        serde_json::from_str(lines.next().expect("error response")).expect("error should be json");
    let health: serde_json::Value = serde_json::from_str(lines.next().expect("health response"))
        .expect("health should be json");

    assert_eq!(error["error"]["code"], "invalid_request");
    assert_eq!(health["request_id"], "health-after-error");
    assert_eq!(health["ok"], true);
    assert!(lines.next().is_none());
}

#[test]
fn oversized_frame_is_drained_before_following_requests() {
    let mut request = vec![b'x'; 64 * 1_024 + 1];
    request.extend_from_slice(
        br#"
{"protocol_version":1,"request_id":"health-after-large","operation":"health"}
"#,
    );
    let output = run_helper(&request);

    assert!(output.status.success());
    let responses = String::from_utf8(output.stdout).expect("responses should be utf-8");
    let mut lines = responses.lines();
    let error: serde_json::Value =
        serde_json::from_str(lines.next().expect("error response")).expect("error should be json");
    let health: serde_json::Value = serde_json::from_str(lines.next().expect("health response"))
        .expect("health should be json");

    assert_eq!(error["error"]["code"], "request_too_large");
    assert_eq!(health["request_id"], "health-after-large");
    assert_eq!(health["ok"], true);
    assert!(lines.next().is_none());
}
