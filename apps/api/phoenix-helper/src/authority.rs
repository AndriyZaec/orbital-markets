use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use phoenix_rise::accounts::trader::TraderHeader;
use phoenix_rise::ix::constants::{PROD_PHOENIX_PROGRAM_ID, phoenix_program_id};
use serde::{Deserialize, Serialize};
use solana_pubkey::Pubkey;

use crate::order::{derive_cross_margin_trader, parse_pubkey};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DecodeAuthorityRequest {
    owner_authority: String,
    trader_account: String,
    account_owner: String,
    data_base64: String,
}

#[derive(Serialize)]
pub struct AuthorityResult {
    pub owner_authority: String,
    pub trader_account: String,
    pub position_authority: String,
    pub trader_pda_index: u8,
    pub subaccount_index: u8,
}

pub fn decode_authority(request: DecodeAuthorityRequest) -> Result<AuthorityResult, String> {
    if phoenix_program_id() != PROD_PHOENIX_PROGRAM_ID
        || request.account_owner != PROD_PHOENIX_PROGRAM_ID.to_string()
    {
        return Err("trader account must be owned by Phoenix mainnet".to_string());
    }
    let owner = parse_pubkey("owner_authority", &request.owner_authority)?;
    let (trader, _) = derive_cross_margin_trader(&owner);
    if request.trader_account != trader.to_string() || request.data_base64.len() > 43_692 {
        return Err("invalid cross trader account or oversized data".to_string());
    }
    let data = STANDARD
        .decode(&request.data_base64)
        .map_err(|_| "invalid trader data base64".to_string())?;
    let header = TraderHeader::try_read_from_account_bytes(&data)
        .map_err(|e| format!("invalid trader header: {e}"))?;
    if header.authority() != owner.as_array()
        || header.trader_pda_index() != 0
        || header.trader_subaccount_index() != 0
    {
        return Err("trader authority or cross account indexes do not match".to_string());
    }
    Ok(AuthorityResult {
        owner_authority: owner.to_string(),
        trader_account: trader.to_string(),
        position_authority: Pubkey::new_from_array(*header.position_authority()).to_string(),
        trader_pda_index: 0,
        subaccount_index: 0,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use phoenix_rise::accounts::PhoenixAccount;
    use std::mem::{offset_of, size_of};

    #[test]
    fn decodes_sdk_header_and_rejects_wrong_owner_scope_and_discriminant() {
        let owner = Pubkey::new_from_array([1; 32]);
        let delegate = Pubkey::new_from_array([2; 32]);
        let (trader, _) = derive_cross_margin_trader(&owner);
        let mut data = vec![0; size_of::<TraderHeader>()];
        data[..8].copy_from_slice(&PhoenixAccount::Trader.discriminant());
        let a = offset_of!(TraderHeader, authority);
        data[a..a + 32].copy_from_slice(owner.as_ref());
        let p = offset_of!(TraderHeader, position_authority);
        data[p..p + 32].copy_from_slice(delegate.as_ref());
        let request = |data: &[u8]| DecodeAuthorityRequest {
            owner_authority: owner.to_string(),
            trader_account: trader.to_string(),
            account_owner: PROD_PHOENIX_PROGRAM_ID.to_string(),
            data_base64: STANDARD.encode(data),
        };
        assert_eq!(
            decode_authority(request(&data)).unwrap().position_authority,
            delegate.to_string()
        );
        data[offset_of!(TraderHeader, trader_subaccount_index)] = 1;
        assert!(decode_authority(request(&data)).is_err());
        data[offset_of!(TraderHeader, trader_subaccount_index)] = 0;
        data[a] ^= 1;
        assert!(decode_authority(request(&data)).is_err());
        data[a] ^= 1;
        data[0] ^= 1;
        assert!(decode_authority(request(&data)).is_err());
        assert!(decode_authority(request(&data[..50])).is_err());
    }
}
