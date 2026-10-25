use std::io::{self, BufRead, BufWriter, Write};

use serde::{Deserialize, Serialize};
use serde_json::Value;

mod order;

const PROTOCOL_VERSION: u32 = 1;
const PHOENIX_RISE_VERSION: &str = "0.6.13";
const MAX_REQUEST_BYTES: usize = 64 * 1_024;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    protocol_version: u32,
    request_id: String,
    operation: String,
    #[serde(default)]
    payload: Option<Value>,
}

#[derive(Serialize)]
struct SuccessResponse<'a, T> {
    protocol_version: u32,
    request_id: &'a str,
    ok: bool,
    result: T,
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

fn read_frame(reader: &mut impl BufRead, frame: &mut Vec<u8>) -> io::Result<Option<bool>> {
    frame.clear();
    let mut oversized = false;

    loop {
        let available = reader.fill_buf()?;
        if available.is_empty() {
            return if frame.is_empty() && !oversized {
                Ok(None)
            } else {
                Ok(Some(oversized))
            };
        }

        let newline = available.iter().position(|byte| *byte == b'\n');
        let consumed = newline.map_or(available.len(), |index| index + 1);
        if !oversized {
            if frame.len() + consumed > MAX_REQUEST_BYTES {
                oversized = true;
            } else {
                frame.extend_from_slice(&available[..consumed]);
            }
        }
        reader.consume(consumed);

        if newline.is_some() {
            return Ok(Some(oversized));
        }
    }
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let stdin = io::stdin();
    let mut input = stdin.lock();
    let mut stdout = BufWriter::new(io::stdout().lock());
    let mut frame = Vec::with_capacity(4_096);

    while let Some(oversized) = read_frame(&mut input, &mut frame)? {
        if oversized {
            write_json_line(
                &mut stdout,
                &ErrorResponse {
                    protocol_version: PROTOCOL_VERSION,
                    request_id: "",
                    ok: false,
                    error: ErrorDetails {
                        code: "request_too_large",
                        message: format!("request exceeds {MAX_REQUEST_BYTES} bytes"),
                    },
                },
            )?;
            continue;
        }
        if frame.iter().all(|byte| byte.is_ascii_whitespace()) {
            continue;
        }
        let request: Request = match serde_json::from_slice(&frame) {
            Ok(request) => request,
            Err(error) => {
                write_json_line(
                    &mut stdout,
                    &ErrorResponse {
                        protocol_version: PROTOCOL_VERSION,
                        request_id: "",
                        ok: false,
                        error: ErrorDetails {
                            code: "invalid_request",
                            message: format!("invalid protocol request: {error}"),
                        },
                    },
                )?;
                continue;
            }
        };
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
            "build_order" => {
                let result = request
                    .payload
                    .ok_or_else(|| "build_order requires payload".to_string())
                    .and_then(|payload| {
                        serde_json::from_value::<order::BuildOrderRequest>(payload)
                            .map_err(|error| format!("invalid build_order payload: {error}"))
                    })
                    .and_then(order::build_order);
                match result {
                    Ok(result) => write_json_line(
                        &mut stdout,
                        &SuccessResponse {
                            protocol_version: PROTOCOL_VERSION,
                            request_id: &request.request_id,
                            ok: true,
                            result,
                        },
                    )?,
                    Err(message) => write_json_line(
                        &mut stdout,
                        &ErrorResponse {
                            protocol_version: PROTOCOL_VERSION,
                            request_id: &request.request_id,
                            ok: false,
                            error: ErrorDetails {
                                code: "invalid_order_request",
                                message,
                            },
                        },
                    )?,
                }
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
