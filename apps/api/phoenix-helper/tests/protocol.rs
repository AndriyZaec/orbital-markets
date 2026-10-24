use std::io::Write;
use std::process::{Command, Stdio};

fn run_helper(input: &[u8]) -> std::process::Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_phoenix-helper"))
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
        br#"{"protocol_version":1,"request_id":"operation-1","operation":"build_order"}
"#,
    );

    assert!(output.status.success());
    assert_eq!(
        String::from_utf8(output.stdout).expect("response should be utf-8"),
        concat!(
            "{\"protocol_version\":1,\"request_id\":\"operation-1\",",
            "\"ok\":false,\"error\":{\"code\":\"unsupported_operation\",",
            "\"message\":\"unsupported operation: build_order\"}}\n"
        )
    );
}
