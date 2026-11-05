//! Flight PDAs use Rise IX 0.6.13. Account layout decoding follows the official
//! TS SDK at c0b1fb879bb1fdb0ce43c6191aba48b5306cd2ac:
//! ts/src/flight/accounts/{BuilderState,GlobalState}/codec.ts.
//! Rise's Rust accounts crate does not expose these two Flight decoders.
use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use phoenix_rise::accounts::trader::TraderHeader;
use phoenix_rise::ix::constants::{PROD_PHOENIX_PROGRAM_ID, phoenix_program_id};
use phoenix_rise::ix::flight::{
    FLIGHT_PROGRAM_ID, flight_builder_state_account_discriminant,
    flight_global_state_account_discriminant, get_flight_builder_state_address,
    get_flight_collateral_transfer_authority_address, get_flight_global_state_address,
};
use serde::{Deserialize, Serialize};
use solana_pubkey::Pubkey;

use crate::order::{derive_cross_margin_trader, parse_pubkey};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FlightRequest {
    builder_authority: String,
}

#[derive(Serialize)]
pub struct FlightAccounts {
    pub phoenix_program_id: String,
    pub flight_program_id: String,
    pub builder_authority: String,
    pub global_state: String,
    pub builder_state: String,
    pub builder_trader_account: String,
    pub collateral_transfer_authority: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AccountData {
    address: String,
    owner: String,
    data_base64: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct VerifyFlightRequest {
    builder_authority: String,
    global_state: AccountData,
    builder_state: AccountData,
    builder_trader: AccountData,
}

#[derive(Serialize)]
pub struct VerifiedFlight {
    accounts: FlightAccounts,
    fee_bps: u64,
    active: bool,
}

pub fn flight_accounts(q: FlightRequest) -> Result<FlightAccounts, String> {
    if phoenix_program_id() != PROD_PHOENIX_PROGRAM_ID {
        return Err("Flight only supports Phoenix mainnet".into());
    }
    let builder = parse_pubkey("builder_authority", &q.builder_authority)?;
    Ok(FlightAccounts {
        phoenix_program_id: PROD_PHOENIX_PROGRAM_ID.to_string(),
        flight_program_id: FLIGHT_PROGRAM_ID.to_string(),
        builder_authority: builder.to_string(),
        global_state: get_flight_global_state_address()
            .map_err(|e| e.to_string())?
            .to_string(),
        builder_state: get_flight_builder_state_address(&builder)
            .map_err(|e| e.to_string())?
            .to_string(),
        builder_trader_account: derive_cross_margin_trader(&builder).0.to_string(),
        collateral_transfer_authority: get_flight_collateral_transfer_authority_address()
            .map_err(|e| e.to_string())?
            .to_string(),
    })
}

fn account(
    q: &AccountData,
    address: &str,
    owner: &str,
    max_size: usize,
) -> Result<Vec<u8>, String> {
    if q.address != address || q.owner != owner || q.data_base64.len() > max_size.div_ceil(3) * 4 {
        return Err("Flight account address, owner or size mismatch".into());
    }
    let bytes = STANDARD
        .decode(&q.data_base64)
        .map_err(|_| "invalid Flight account base64")?;
    if bytes.len() > max_size {
        return Err("oversized Flight account".into());
    }
    Ok(bytes)
}

fn key(bytes: &[u8]) -> Pubkey {
    Pubkey::new_from_array(
        bytes
            .try_into()
            .expect("validated fixed-size account field"),
    )
}
fn u64_field(bytes: &[u8]) -> u64 {
    u64::from_le_bytes(
        bytes
            .try_into()
            .expect("validated fixed-size account field"),
    )
}

pub fn verify_flight(q: VerifyFlightRequest) -> Result<VerifiedFlight, String> {
    let accounts = flight_accounts(FlightRequest {
        builder_authority: q.builder_authority,
    })?;
    // Hidden discriminator prefix + fixedSize in the official codecs: 368/216.
    let global = account(
        &q.global_state,
        &accounts.global_state,
        &accounts.flight_program_id,
        368,
    )?;
    let builder = account(
        &q.builder_state,
        &accounts.builder_state,
        &accounts.flight_program_id,
        216,
    )?;
    let trader = account(
        &q.builder_trader,
        &accounts.builder_trader_account,
        &accounts.phoenix_program_id,
        32_768,
    )?;
    if global.len() != 368
        || global[..8] != flight_global_state_account_discriminant()
        || key(&global[8..40]) != PROD_PHOENIX_PROGRAM_ID
        || u64_field(&global[104..112]) < 2
    {
        return Err("invalid Flight global state or fee cap".into());
    }
    if builder.len() != 216
        || builder[..8] != flight_builder_state_account_discriminant()
        || key(&builder[8..40]).to_string() != accounts.builder_authority
        || key(&builder[40..72]).to_string() != accounts.builder_trader_account
        || u64_field(&builder[72..80]) & 1 == 0
        || u64_field(&builder[80..88]) != 2
    {
        return Err(
            "Flight builder must be active with registered fee exactly 2 bps and cross trader 0/0"
                .into(),
        );
    }
    let header = TraderHeader::try_read_from_account_bytes(&trader)
        .map_err(|e| format!("invalid Flight builder trader: {e}"))?;
    if Pubkey::new_from_array(*header.authority()).to_string() != accounts.builder_authority
        || header.trader_pda_index() != 0
        || header.trader_subaccount_index() != 0
    {
        return Err("Flight builder trader authority or indexes mismatch".into());
    }
    Ok(VerifiedFlight {
        accounts,
        fee_bps: 2,
        active: true,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use phoenix_rise::accounts::PhoenixAccount;
    use std::mem::{offset_of, size_of};

    fn fixture() -> VerifyFlightRequest {
        let authority = Pubkey::new_from_array([4; 32]);
        let addresses = flight_accounts(FlightRequest {
            builder_authority: authority.to_string(),
        })
        .unwrap();
        let mut global = vec![0; 368];
        global[..8].copy_from_slice(&flight_global_state_account_discriminant());
        global[8..40].copy_from_slice(PROD_PHOENIX_PROGRAM_ID.as_ref());
        global[104..112].copy_from_slice(&100_u64.to_le_bytes());
        let mut builder = vec![0; 216];
        builder[..8].copy_from_slice(&flight_builder_state_account_discriminant());
        builder[8..40].copy_from_slice(authority.as_ref());
        builder[40..72].copy_from_slice(derive_cross_margin_trader(&authority).0.as_ref());
        builder[72..80].copy_from_slice(&1_u64.to_le_bytes());
        builder[80..88].copy_from_slice(&2_u64.to_le_bytes());
        let mut trader = vec![0; size_of::<TraderHeader>()];
        trader[..8].copy_from_slice(&PhoenixAccount::Trader.discriminant());
        trader[offset_of!(TraderHeader, authority)..offset_of!(TraderHeader, authority) + 32]
            .copy_from_slice(authority.as_ref());
        VerifyFlightRequest {
            builder_authority: authority.to_string(),
            global_state: AccountData {
                address: addresses.global_state,
                owner: addresses.flight_program_id.clone(),
                data_base64: STANDARD.encode(global),
            },
            builder_state: AccountData {
                address: addresses.builder_state,
                owner: addresses.flight_program_id,
                data_base64: STANDARD.encode(builder),
            },
            builder_trader: AccountData {
                address: addresses.builder_trader_account,
                owner: addresses.phoenix_program_id,
                data_base64: STANDARD.encode(trader),
            },
        }
    }

    #[test]
    fn validates_chain_registration_instead_of_an_order_manifest() {
        assert_eq!(verify_flight(fixture()).unwrap().fee_bps, 2);
        for offset in [0, 8, 40, 72, 80] {
            let mut q = fixture();
            let mut data = STANDARD.decode(&q.builder_state.data_base64).unwrap();
            data[offset] ^= 1;
            q.builder_state.data_base64 = STANDARD.encode(data);
            assert!(verify_flight(q).is_err(), "builder field {offset}");
        }
        for offset in [0, 8, 104] {
            let mut q = fixture();
            let mut data = STANDARD.decode(&q.global_state.data_base64).unwrap();
            data[offset] = 0;
            q.global_state.data_base64 = STANDARD.encode(data);
            assert!(verify_flight(q).is_err(), "global field {offset}");
        }
        let mut q = fixture();
        q.builder_state.owner = PROD_PHOENIX_PROGRAM_ID.to_string();
        assert!(verify_flight(q).is_err());
        let mut q = fixture();
        q.builder_trader.address = q.builder_authority.clone();
        assert!(verify_flight(q).is_err());
        let mut q = fixture();
        q.builder_trader.data_base64 = STANDARD.encode([0; 50]);
        assert!(verify_flight(q).is_err());
        let mut q = fixture();
        q.builder_state.data_base64 = STANDARD.encode([0; 215]);
        assert!(verify_flight(q).is_err());
    }
}
