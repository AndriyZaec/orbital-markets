use std::io::{self, BufRead, BufWriter, Write};

use serde::{Deserialize, Serialize};

const PROTOCOL_VERSION: u32 = 1;
const PHOENIX_RISE_VERSION: &str = "0.6.13";

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    protocol_version: u32,
    request_id: String,
    operation: String,
}

#[derive(Serialize)]
struct SuccessResponse<'a> {
    protocol_version: u32,
    request_id: &'a str,
    ok: bool,
    result: HealthResult<'a>,
}

#[derive(Serialize)]
struct HealthResult<'a> {
    helper_version: &'a str,
    phoenix_rise_version: &'a str,
}

#[derive(Serialize)]
struct ErrorResponse<'a> {
    protocol_version: u32,
    request_id: &'a str,
    ok: bool,
    error: ErrorDetails<'a>,
}

#[derive(Serialize)]
struct ErrorDetails<'a> {
    code: &'a str,
    message: String,
}

fn write_json_line(
    writer: &mut impl Write,
    value: &impl Serialize,
) -> Result<(), Box<dyn std::error::Error>> {
    serde_json::to_writer(&mut *writer, value)?;
    writer.write_all(b"\n")?;
    writer.flush()?;
    Ok(())
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let stdin = io::stdin();
    let mut stdout = BufWriter::new(io::stdout().lock());

    for line in stdin.lock().lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }

        let request: Request = serde_json::from_str(&line)?;
        if request.protocol_version != PROTOCOL_VERSION {
            write_json_line(
                &mut stdout,
                &ErrorResponse {
                    protocol_version: PROTOCOL_VERSION,
                    request_id: &request.request_id,
                    ok: false,
                    error: ErrorDetails {
                        code: "unsupported_protocol_version",
                        message: format!("expected protocol version {PROTOCOL_VERSION}"),
                    },
                },
            )?;
            continue;
        }

        match request.operation.as_str() {
            "health" => {
                write_json_line(
                    &mut stdout,
                    &SuccessResponse {
                        protocol_version: PROTOCOL_VERSION,
                        request_id: &request.request_id,
                        ok: true,
                        result: HealthResult {
                            helper_version: env!("CARGO_PKG_VERSION"),
                            phoenix_rise_version: PHOENIX_RISE_VERSION,
                        },
                    },
                )?;
            }
            operation => {
                write_json_line(
                    &mut stdout,
                    &ErrorResponse {
                        protocol_version: PROTOCOL_VERSION,
                        request_id: &request.request_id,
                        ok: false,
                        error: ErrorDetails {
                            code: "unsupported_operation",
                            message: format!("unsupported operation: {operation}"),
                        },
                    },
                )?;
            }
        }
    }

    Ok(())
}
