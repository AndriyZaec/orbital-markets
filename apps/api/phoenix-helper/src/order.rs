use std::str::FromStr;

use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use phoenix_rise::ix::constants::{PROD_PHOENIX_PROGRAM_ID, phoenix_program_id};
use phoenix_rise::ix::flight::{
    FLIGHT_PROGRAM_ID, ProxyInstructionParams, create_proxy_instruction_ix,
    get_flight_builder_state_address,
};
use phoenix_rise::ix::market_order::{MarketOrderParams, create_place_market_order_ix};
use phoenix_rise::ix::types::{OrderFlags, SelfTradeBehavior, Side as PhoenixSide};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use solana_hash::Hash;
use solana_message::legacy::Message;
use solana_pubkey::Pubkey;

const MAX_TRANSACTION_BYTES: usize = 1_232;
const EXPECTED_FLIGHT_FEE_BPS: u64 = 2;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BuildOrderRequest {
    action: Action,
    owner_authority: String,
    position_authority: String,
    fee_payer: String,
    builder_authority: Option<String>,
    symbol: String,
    side: Side,
    num_base_lots: u64,
    price_limit_ticks: u64,
    reduce_only: bool,
    client_order_id: u64,
    last_valid_slot: u64,
    recent_blockhash: String,
    last_valid_block_height: u64,
    metadata_generation: u64,
    accounts: MarketAccounts,
}

#[derive(Clone, Copy, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
enum Action {
    Open,
    Close,
    Unwind,
    EmergencyClose,
}

impl Action {
    fn uses_flight(self) -> bool {
        matches!(self, Self::Open | Self::Close)
    }
}

#[derive(Clone, Copy, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
enum Side {
    Bid,
    Ask,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MarketAccounts {
    perp_asset_map: String,
    orderbook: String,
    spline_collection: String,
    global_trader_index: Vec<String>,
    active_trader_buffer: Vec<String>,
}

#[derive(Serialize)]
pub struct BuildOrderResult {
    message_base64: String,
    message_sha256: String,
    required_signers: Vec<RequiredSigner>,
    recent_blockhash: String,
    last_valid_block_height: u64,
    metadata_generation: u64,
    trader_account: String,
    trader_pda_index: u8,
    subaccount_index: u8,
    trader_bump: u8,
    manifest: OrderManifest,
}

#[derive(Serialize)]
struct RequiredSigner {
    index: usize,
    pubkey: String,
    role: &'static str,
}

#[derive(Serialize)]
struct OrderManifest {
    format: &'static str,
    phoenix_program_id: String,
    routing: Routing,
    flight: Option<FlightManifest>,
    intent_action: Action,
    owner_authority: String,
    position_authority: String,
    fee_payer: String,
    trader_account: String,
    requested_symbol: String,
    orderbook: String,
    metadata_generation: u64,
    side: Side,
    num_base_lots: u64,
    price_limit_ticks: u64,
    reduce_only: bool,
    client_order_id: u64,
    last_valid_slot: u64,
    account_keys: Vec<AccountManifest>,
    instructions: Vec<InstructionManifest>,
}

#[derive(Serialize)]
#[serde(rename_all = "snake_case")]
enum Routing {
    Native,
    Flight,
}

#[derive(Serialize)]
struct FlightManifest {
    flight_program_id: String,
    builder_authority: String,
    builder_trader_account: String,
    builder_state: String,
    builder_pda_index: u8,
    builder_subaccount_index: u8,
    expected_registered_fee_bps: u64,
    uses_fee_override: bool,
}

#[derive(Serialize)]
struct AccountManifest {
    index: usize,
    pubkey: String,
    signer: bool,
    writable: bool,
}

#[derive(Serialize)]
struct InstructionManifest {
    program_id: String,
    program_id_index: u8,
    account_indexes: Vec<u8>,
    data_base64: String,
}

pub fn build_order(request: BuildOrderRequest) -> Result<BuildOrderResult, String> {
    if phoenix_program_id() != PROD_PHOENIX_PROGRAM_ID {
        return Err("build_order only supports Phoenix mainnet".to_string());
    }
    if request.symbol.is_empty() || request.symbol.len() > 32 {
        return Err("symbol must contain between 1 and 32 bytes".to_string());
    }
    if request.num_base_lots == 0 {
        return Err("num_base_lots must be greater than zero".to_string());
    }
    if request.price_limit_ticks == 0 {
        return Err("price_limit_ticks must be greater than zero".to_string());
    }
    if request.accounts.global_trader_index.is_empty()
        || request.accounts.active_trader_buffer.is_empty()
    {
        return Err("market metadata account lists must not be empty".to_string());
    }
    if request.accounts.global_trader_index.len() > 64
        || request.accounts.active_trader_buffer.len() > 64
    {
        return Err("market metadata account lists must contain at most 64 entries".to_string());
    }
    if matches!(request.action, Action::Open) == request.reduce_only {
        return Err("open must not be reduce-only; safety actions must be reduce-only".to_string());
    }

    let owner_authority = parse_pubkey("owner_authority", &request.owner_authority)?;
    let position_authority = parse_pubkey("position_authority", &request.position_authority)?;
    let fee_payer = parse_pubkey("fee_payer", &request.fee_payer)?;
    if owner_authority == position_authority {
        return Err("position_authority must differ from owner_authority".to_string());
    }
    if fee_payer == position_authority {
        return Err("fee_payer must differ from position_authority".to_string());
    }

    let recent_blockhash = Hash::from_str(&request.recent_blockhash)
        .map_err(|error| format!("invalid recent_blockhash: {error}"))?;
    let perp_asset_map = parse_pubkey("accounts.perp_asset_map", &request.accounts.perp_asset_map)?;
    let orderbook = parse_pubkey("accounts.orderbook", &request.accounts.orderbook)?;
    let spline_collection = parse_pubkey(
        "accounts.spline_collection",
        &request.accounts.spline_collection,
    )?;
    let global_trader_index = parse_pubkeys(
        "accounts.global_trader_index",
        &request.accounts.global_trader_index,
    )?;
    let active_trader_buffer = parse_pubkeys(
        "accounts.active_trader_buffer",
        &request.accounts.active_trader_buffer,
    )?;

    let (trader_account, trader_bump) = derive_cross_margin_trader(&owner_authority);
    let side = match request.side {
        Side::Bid => PhoenixSide::Bid,
        Side::Ask => PhoenixSide::Ask,
    };
    let order_flags = if request.reduce_only {
        OrderFlags::ReduceOnly
    } else {
        OrderFlags::None
    };

    let params = MarketOrderParams::builder()
        .trader(position_authority)
        .trader_account(trader_account)
        .perp_asset_map(perp_asset_map)
        .orderbook(orderbook)
        .spline_collection(spline_collection)
        .global_trader_index(global_trader_index)
        .active_trader_buffer(active_trader_buffer)
        .side(side)
        .price_in_ticks(request.price_limit_ticks)
        .num_base_lots(request.num_base_lots)
        .self_trade_behavior(SelfTradeBehavior::Abort)
        .client_order_id(u128::from(request.client_order_id))
        .last_valid_slot(request.last_valid_slot)
        .order_flags(order_flags)
        .cancel_existing(false)
        .symbol(&request.symbol)
        .subaccount_index(0)
        .build()
        .map_err(|error| format!("invalid market order: {error}"))?;
    let phoenix_instruction = create_place_market_order_ix(params)
        .map_err(|error| format!("failed to build market order: {error}"))?;
    let (instruction, routing, flight) = if request.action.uses_flight() {
        let builder_authority = request
            .builder_authority
            .as_deref()
            .ok_or_else(|| "normal open and close require builder_authority".to_string())
            .and_then(|value| parse_pubkey("builder_authority", value))?;
        let (builder_trader_account, _) = derive_cross_margin_trader(&builder_authority);
        let builder_state = get_flight_builder_state_address(&builder_authority)
            .map_err(|error| format!("failed to derive Flight builder state: {error}"))?;
        let proxy_params = ProxyInstructionParams::builder()
            .builder_authority(builder_authority)
            .builder_trader_account(builder_trader_account)
            .trader_wallet(position_authority)
            .root_authority(owner_authority)
            .inner_instruction(phoenix_instruction)
            .build()
            .map_err(|error| format!("invalid Flight proxy order: {error}"))?;
        let proxy_instruction = create_proxy_instruction_ix(proxy_params)
            .map_err(|error| format!("failed to build Flight proxy order: {error}"))?;
        (
            proxy_instruction.into(),
            Routing::Flight,
            Some(FlightManifest {
                flight_program_id: FLIGHT_PROGRAM_ID.to_string(),
                builder_authority: builder_authority.to_string(),
                builder_trader_account: builder_trader_account.to_string(),
                builder_state: builder_state.to_string(),
                builder_pda_index: 0,
                builder_subaccount_index: 0,
                expected_registered_fee_bps: EXPECTED_FLIGHT_FEE_BPS,
                uses_fee_override: false,
            }),
        )
    } else {
        if request.builder_authority.is_some() {
            return Err("unwind and emergency_close must not use builder_authority".to_string());
        }
        (
            solana_instruction::Instruction::from(phoenix_instruction),
            Routing::Native,
            None,
        )
    };
    let message = Message::new_with_blockhash(&[instruction], Some(&fee_payer), &recent_blockhash);
    let message_bytes = bincode::serialize(&message)
        .map_err(|error| format!("failed to serialize message: {error}"))?;
    let required_signatures = usize::from(message.header.num_required_signatures);
    let transaction_size = 1 + required_signatures * 64 + message_bytes.len();
    if transaction_size > MAX_TRANSACTION_BYTES {
        return Err(format!(
            "transaction template is {transaction_size} bytes; maximum is {MAX_TRANSACTION_BYTES}"
        ));
    }

    let required_signers = message.account_keys[..required_signatures]
        .iter()
        .enumerate()
        .map(|(index, pubkey)| {
            let role = if *pubkey == fee_payer {
                "fee_payer"
            } else if *pubkey == position_authority {
                "position_authority"
            } else {
                "unexpected"
            };
            RequiredSigner {
                index,
                pubkey: pubkey.to_string(),
                role,
            }
        })
        .collect::<Vec<_>>();
    if required_signers.len() != 2
        || required_signers
            .iter()
            .any(|signer| signer.role == "unexpected")
    {
        return Err("message requires an unexpected signer set".to_string());
    }

    let account_keys = message
        .account_keys
        .iter()
        .enumerate()
        .map(|(index, pubkey)| {
            let signer = index < required_signatures;
            let writable = if signer {
                index
                    < required_signatures - usize::from(message.header.num_readonly_signed_accounts)
            } else {
                index
                    < message.account_keys.len()
                        - usize::from(message.header.num_readonly_unsigned_accounts)
            };
            AccountManifest {
                index,
                pubkey: pubkey.to_string(),
                signer,
                writable,
            }
        })
        .collect();
    let instructions = message
        .instructions
        .iter()
        .map(|instruction| InstructionManifest {
            program_id: message.account_keys[usize::from(instruction.program_id_index)].to_string(),
            program_id_index: instruction.program_id_index,
            account_indexes: instruction.accounts.clone(),
            data_base64: STANDARD.encode(&instruction.data),
        })
        .collect();
    let message_sha256 = format!("{:x}", Sha256::digest(&message_bytes));

    Ok(BuildOrderResult {
        message_base64: STANDARD.encode(message_bytes),
        message_sha256,
        required_signers,
        recent_blockhash: request.recent_blockhash,
        last_valid_block_height: request.last_valid_block_height,
        metadata_generation: request.metadata_generation,
        trader_account: trader_account.to_string(),
        trader_pda_index: 0,
        subaccount_index: 0,
        trader_bump,
        manifest: OrderManifest {
            format: "solana_legacy_message",
            phoenix_program_id: PROD_PHOENIX_PROGRAM_ID.to_string(),
            routing,
            flight,
            intent_action: request.action,
            owner_authority: owner_authority.to_string(),
            position_authority: position_authority.to_string(),
            fee_payer: fee_payer.to_string(),
            trader_account: trader_account.to_string(),
            requested_symbol: request.symbol,
            orderbook: orderbook.to_string(),
            metadata_generation: request.metadata_generation,
            side: request.side,
            num_base_lots: request.num_base_lots,
            price_limit_ticks: request.price_limit_ticks,
            reduce_only: request.reduce_only,
            client_order_id: request.client_order_id,
            last_valid_slot: request.last_valid_slot,
            account_keys,
            instructions,
        },
    })
}

fn parse_pubkey(field: &str, value: &str) -> Result<Pubkey, String> {
    Pubkey::from_str(value).map_err(|error| format!("invalid {field}: {error}"))
}

fn derive_cross_margin_trader(authority: &Pubkey) -> (Pubkey, u8) {
    let pda_schema = [0_u8, 0_u8];
    Pubkey::find_program_address(
        &[b"trader", authority.as_ref(), &pda_schema],
        &PROD_PHOENIX_PROGRAM_ID,
    )
}

fn parse_pubkeys(field: &str, values: &[String]) -> Result<Vec<Pubkey>, String> {
    values
        .iter()
        .map(|value| parse_pubkey(field, value))
        .collect()
}
