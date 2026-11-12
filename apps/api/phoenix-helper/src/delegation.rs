use std::str::FromStr;

use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use phoenix_rise::ix::constants::{PROD_PHOENIX_PROGRAM_ID, phoenix_program_id};
use phoenix_rise::ix::delegate_trader::{DelegateTraderParams, create_delegate_trader_ix};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use solana_hash::Hash;
use solana_message::legacy::Message;

use crate::compute::ComputeBudget;
use crate::message::{AccountManifest, InstructionManifest, RequiredSigner, describe};
use crate::order::{derive_cross_margin_trader, parse_pubkey};

#[derive(Clone, Copy, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum DelegationAction {
    Delegate,
    Revoke,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BuildDelegationRequest {
    action: DelegationAction,
    owner_authority: String,
    new_position_authority: String,
    fee_payer: String,
    recent_blockhash: String,
    last_valid_block_height: u64,
}

#[derive(Serialize)]
pub struct BuildDelegationResult {
    message_base64: String,
    message_sha256: String,
    required_signers: Vec<RequiredSigner>,
    recent_blockhash: String,
    last_valid_block_height: u64,
    trader_account: String,
    trader_pda_index: u8,
    subaccount_index: u8,
    trader_bump: u8,
    manifest: DelegationManifest,
}

#[derive(Serialize)]
struct DelegationManifest {
    format: &'static str,
    phoenix_program_id: String,
    action: DelegationAction,
    owner_authority: String,
    new_position_authority: String,
    fee_payer: String,
    trader_account: String,
    account_keys: Vec<AccountManifest>,
    instructions: Vec<InstructionManifest>,
}

pub fn build_delegation(request: BuildDelegationRequest) -> Result<BuildDelegationResult, String> {
    if phoenix_program_id() != PROD_PHOENIX_PROGRAM_ID {
        return Err("build_delegation only supports Phoenix mainnet".to_string());
    }
    let owner = parse_pubkey("owner_authority", &request.owner_authority)?;
    let target = parse_pubkey("new_position_authority", &request.new_position_authority)?;
    let payer = parse_pubkey("fee_payer", &request.fee_payer)?;
    if payer == owner || payer == target {
        return Err("fee_payer must differ from owner and new position authority".to_string());
    }
    match request.action {
        DelegationAction::Delegate if target == owner => {
            return Err("delegate must target a separate position authority".to_string());
        }
        DelegationAction::Revoke if target != owner => {
            return Err("revoke must restore owner position authority".to_string());
        }
        _ => {}
    }
    if request.last_valid_block_height == 0 {
        return Err("last_valid_block_height must be positive".to_string());
    }
    let hash = Hash::from_str(&request.recent_blockhash)
        .map_err(|e| format!("invalid recent_blockhash: {e}"))?;
    let (trader, bump) = derive_cross_margin_trader(&owner);
    let params = DelegateTraderParams::builder()
        .trader_wallet(owner)
        .trader_account(trader)
        .new_position_authority(target)
        .build()
        .map_err(|e| format!("invalid delegation: {e}"))?;
    let instruction = create_delegate_trader_ix(params)
        .map_err(|e| format!("failed to build delegation: {e}"))?;
    // Explicitly pin the default single-instruction allowance and zero priority
    // fee so wallets do not enhance the immutable owner-signing message.
    let [limit, price] = ComputeBudget {
        unit_limit: 200_000,
        unit_price_micro_lamports: 0,
    }
    .instructions()?;
    let message =
        Message::new_with_blockhash(&[limit, price, instruction.into()], Some(&payer), &hash);
    if message.header.num_required_signatures != 2
        || message.account_keys[0] != payer
        || message.account_keys[1] != owner
    {
        return Err("delegation requires fee payer and owner signers only".to_string());
    }
    let bytes =
        bincode::serialize(&message).map_err(|e| format!("failed to serialize delegation: {e}"))?;
    if 1 + 2 * 64 + bytes.len() > 1232 {
        return Err("delegation exceeds transaction packet limit".to_string());
    }
    let (account_keys, instructions) = describe(&message);
    Ok(BuildDelegationResult {
        message_base64: STANDARD.encode(&bytes),
        message_sha256: format!("{:x}", Sha256::digest(&bytes)),
        required_signers: vec![
            RequiredSigner {
                index: 0,
                pubkey: payer.to_string(),
                role: "fee_payer",
            },
            RequiredSigner {
                index: 1,
                pubkey: owner.to_string(),
                role: "owner_authority",
            },
        ],
        recent_blockhash: request.recent_blockhash,
        last_valid_block_height: request.last_valid_block_height,
        trader_account: trader.to_string(),
        trader_pda_index: 0,
        subaccount_index: 0,
        trader_bump: bump,
        manifest: DelegationManifest {
            format: "solana_legacy_message",
            phoenix_program_id: PROD_PHOENIX_PROGRAM_ID.to_string(),
            action: request.action,
            owner_authority: owner.to_string(),
            new_position_authority: target.to_string(),
            fee_payer: payer.to_string(),
            trader_account: trader.to_string(),
            account_keys,
            instructions,
        },
    })
}
