//! Decode observed transaction events with pinned Rise, never fee estimates.
use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use phoenix_rise::ix::constants::PROD_PHOENIX_PROGRAM_ID;
use phoenix_rise::ix::flight::{
    FLIGHT_PROGRAM_ID, get_flight_collateral_transfer_authority_address,
};
use phoenix_rise_events::{
    MarketEvent, PhoenixLogInstruction, PhoenixLogInstructionKind, parse_with_errors,
};
use serde::{Deserialize, Serialize};
use solana_message::legacy::Message;

use crate::order::{derive_cross_margin_trader, parse_pubkey};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AccountingRequest {
    owner_authority: String,
    builder_authority: Option<String>,
    expected_message_base64: String,
    transaction_base64: String,
    inner_instructions: Vec<InnerGroup>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct InnerGroup {
    index: u32,
    instructions: Vec<InnerInstruction>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct InnerInstruction {
    program_id_index: u16,
    accounts: Vec<u16>,
    data: String,
    stack_height: Option<u32>,
}

#[derive(Serialize)]
pub struct AccountingResult {
    owner_authority: String,
    trader_account: String,
    flight_fee_quote_lots: Option<String>,
    flight_transfer_events: usize,
    decoded_event_count: usize,
    funding_settlements: Vec<FundingSettlement>,
}
#[derive(Serialize)]
struct FundingSettlement {
    event_index: usize,
    symbol: String,
    asset_id: u32,
    payment_quote_lots: String,
}

pub fn decode(q: AccountingRequest) -> Result<AccountingResult, String> {
    let owner = parse_pubkey("owner_authority", &q.owner_authority)?;
    let trader = derive_cross_margin_trader(&owner).0;
    if q.transaction_base64.len() > 1644
        || q.expected_message_base64.len() > 1472
        || q.inner_instructions.len() != 1
    {
        return Err("invalid accounting packet or instruction group bound".into());
    }
    let packet = STANDARD
        .decode(&q.transaction_base64)
        .map_err(|_| "invalid accounting packet base64")?;
    let expected = STANDARD
        .decode(&q.expected_message_base64)
        .map_err(|_| "invalid stored message base64")?;
    if packet.len() <= 129 || packet.len() > 1232 || packet[0] != 2 || packet[129..] != expected {
        return Err("accounting packet differs from stored message".into());
    }
    let message: Message =
        bincode::deserialize(&expected).map_err(|_| "invalid legacy accounting message")?;
    if message.header.num_required_signatures != 2 || message.instructions.len() != 1 {
        return Err("unexpected accounting message/signers".into());
    }
    let top = &message.instructions[0];
    let program = message
        .account_keys
        .get(usize::from(top.program_id_index))
        .ok_or("invalid top program index")?;
    let builder = q
        .builder_authority
        .as_ref()
        .map(|v| parse_pubkey("builder_authority", v))
        .transpose()?;
    if *program
        != if builder.is_some() {
            FLIGHT_PROGRAM_ID
        } else {
            PROD_PHOENIX_PROGRAM_ID
        }
    {
        return Err("accounting routing does not match stored message".into());
    }
    let collector = builder.map(|key| derive_cross_margin_trader(&key).0);
    let transfer_authority =
        get_flight_collateral_transfer_authority_address().map_err(|e| e.to_string())?;
    let group = &q.inner_instructions[0];
    if group.index != 0 || group.instructions.len() > 512 {
        return Err("accounting outer instruction mismatch".into());
    }
    let mut raw_logs = Vec::new();
    for ix in &group.instructions {
        if ix
            .accounts
            .iter()
            .any(|&i| usize::from(i) >= message.account_keys.len())
            || ix.stack_height.is_some_and(|height| height < 2)
        {
            return Err("invalid accounting inner account/stack layout".into());
        }
        let program = message
            .account_keys
            .get(usize::from(ix.program_id_index))
            .ok_or("invalid inner program index")?;
        if *program != PROD_PHOENIX_PROGRAM_ID {
            continue;
        }
        if ix.data.len() > 65_536 {
            return Err("oversized accounting instruction".into());
        }
        let data = bs58::decode(&ix.data)
            .into_vec()
            .map_err(|_| "invalid accounting instruction base58")?;
        if PhoenixLogInstruction::from_instruction_data(&data).is_some() {
            raw_logs.push(data);
        }
    }
    if raw_logs.is_empty() {
        return Err("missing Phoenix log evidence".into());
    }
    let logs: Vec<_> = raw_logs
        .iter()
        .filter_map(|data| PhoenixLogInstruction::from_instruction_data(data))
        .collect();
    let mut expected_events = 0_usize;
    let log_count = logs
        .iter()
        .filter(|log| log.kind == PhoenixLogInstructionKind::Log)
        .count();
    let lengths_count = logs.len() - log_count;
    if log_count == 0 || (lengths_count > 0 && lengths_count != log_count) {
        return Err("incomplete accounting log batches".into());
    }
    for log in &logs {
        if log.kind == PhoenixLogInstructionKind::Log {
            let bytes = log.data.get(4..8).ok_or("truncated Phoenix log header")?;
            expected_events = expected_events
                .checked_add(
                    u32::from_le_bytes(bytes.try_into().map_err(|_| "invalid log count")?) as usize,
                )
                .ok_or("event count overflow")?;
        }
    }
    if expected_events == 0 || expected_events > 1024 {
        return Err("accounting event count exceeds bound".into());
    }
    let decoded = parse_with_errors(logs);
    if !decoded.failures.is_empty()
        || !decoded.skipped_event_bytes.is_empty()
        || decoded.events.len() != expected_events
    {
        return Err("incomplete or unknown Phoenix event evidence".into());
    }
    let mut total = 0_u64;
    let mut transfers = 0;
    let mut settlements = Vec::new();
    for (index, event) in decoded.events.iter().enumerate() {
        match event {
            MarketEvent::TraderCollateralTransferred(value)
                if value.src_trader == trader.to_bytes()
                    && collector.is_some_and(|key| value.dst_trader == key.to_bytes())
                    && value.authority == transfer_authority.to_bytes() =>
            {
                total = total
                    .checked_add(value.amount.as_inner())
                    .ok_or("Flight fee overflow")?;
                transfers += 1;
            }
            MarketEvent::TraderFundingSettled(value) if value.trader == trader.to_bytes() => {
                let bytes = value.asset_symbol.to_bytes();
                let end = bytes.iter().position(|&b| b == 0).unwrap_or(bytes.len());
                let symbol =
                    std::str::from_utf8(&bytes[..end]).map_err(|_| "invalid settlement symbol")?;
                if symbol.is_empty() {
                    return Err("empty funding settlement symbol".into());
                }
                settlements.push(FundingSettlement {
                    event_index: index,
                    symbol: symbol.into(),
                    asset_id: value.asset_id,
                    payment_quote_lots: value.funding_payment.as_inner().to_string(),
                });
            }
            _ => {}
        }
    }
    // Missing fee transfer is unknown for Flight, not an assumed zero. Native
    // safety routing cannot charge the configured Flight builder through a proxy.
    let fee = if collector.is_none() || transfers > 0 {
        Some(total.to_string())
    } else {
        None
    };
    Ok(AccountingResult {
        owner_authority: owner.to_string(),
        trader_account: trader.to_string(),
        flight_fee_quote_lots: fee,
        flight_transfer_events: transfers,
        decoded_event_count: decoded.events.len(),
        funding_settlements: settlements,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use phoenix_rise_events::market_events::{
        QuoteLots, SignedQuoteLots, SignedQuoteLotsPerBaseLot, Symbol,
        TraderCollateralTransferredEvent, TraderFundingSettledEvent,
    };
    use solana_hash::Hash;
    use solana_instruction::{AccountMeta, Instruction};
    use solana_pubkey::Pubkey;

    fn fixture() -> AccountingRequest {
        let owner = Pubkey::new_from_array([1; 32]);
        let builder = Pubkey::new_from_array([4; 32]);
        let trader = derive_cross_margin_trader(&owner).0;
        let collector = derive_cross_margin_trader(&builder).0;
        let transfer = get_flight_collateral_transfer_authority_address().unwrap();
        let events = vec![
            MarketEvent::TraderCollateralTransferred(TraderCollateralTransferredEvent {
                authority: transfer.to_bytes(),
                amount: QuoteLots::new(12345),
                src_trader: trader.to_bytes(),
                src_trader_sequence_number: 1,
                src_trader_prev_sequence_number_slot: 0,
                src_trader_new_collateral_balance: SignedQuoteLots::new(100),
                dst_trader: collector.to_bytes(),
                dst_trader_sequence_number: 2,
                dst_trader_prev_sequence_number_slot: 0,
                dst_trader_new_collateral_balance: SignedQuoteLots::new(12345),
            }),
            MarketEvent::TraderFundingSettled(TraderFundingSettledEvent {
                trader: trader.to_bytes(),
                asset_symbol: Symbol::new("SOL").unwrap(),
                asset_id: 2,
                funding_payment: SignedQuoteLots::new(-77),
                cumulative_funding_snapshot: SignedQuoteLotsPerBaseLot::new(0),
                new_collateral_balance: SignedQuoteLots::new(100),
            }),
        ];
        let payer = Pubkey::new_from_array([3; 32]);
        let agent = Pubkey::new_from_array([2; 32]);
        let ix = Instruction {
            program_id: FLIGHT_PROGRAM_ID,
            accounts: vec![
                AccountMeta::new_readonly(agent, true),
                AccountMeta::new_readonly(PROD_PHOENIX_PROGRAM_ID, false),
            ],
            data: vec![],
        };
        let message =
            Message::new_with_blockhash(&[ix], Some(&payer), &Hash::new_from_array([9; 32]));
        let bytes = bincode::serialize(&message).unwrap();
        let mut packet = vec![0; 129];
        packet[0] = 2;
        packet.extend_from_slice(&bytes);
        let mut lengths = PhoenixLogInstructionKind::LogEventLengths
            .discriminant()
            .to_vec();
        lengths.extend_from_slice(&0_u32.to_le_bytes());
        lengths.extend_from_slice(&(events.len() as u32).to_le_bytes());
        let mut log = PhoenixLogInstructionKind::Log.discriminant().to_vec();
        log.extend_from_slice(&0_u32.to_le_bytes());
        log.extend_from_slice(&(events.len() as u32).to_le_bytes());
        for event in events {
            let data = borsh::to_vec(&event).unwrap();
            lengths.extend_from_slice(&(data.len() as u16).to_le_bytes());
            log.extend(data);
        }
        let program_index = message
            .account_keys
            .iter()
            .position(|key| *key == PROD_PHOENIX_PROGRAM_ID)
            .unwrap() as u16;
        AccountingRequest {
            owner_authority: owner.to_string(),
            builder_authority: Some(builder.to_string()),
            expected_message_base64: STANDARD.encode(bytes),
            transaction_base64: STANDARD.encode(packet),
            inner_instructions: vec![InnerGroup {
                index: 0,
                instructions: vec![
                    InnerInstruction {
                        program_id_index: program_index,
                        accounts: vec![],
                        data: bs58::encode(lengths).into_string(),
                        stack_height: Some(2),
                    },
                    InnerInstruction {
                        program_id_index: program_index,
                        accounts: vec![],
                        data: bs58::encode(log).into_string(),
                        stack_height: Some(2),
                    },
                ],
            }],
        }
    }
    #[test]
    fn observed_transfer_not_registered_rate_defines_fee() {
        let result = decode(fixture()).unwrap();
        assert_eq!(result.flight_fee_quote_lots.as_deref(), Some("12345"));
        assert_eq!(result.flight_transfer_events, 1);
        assert_eq!(result.funding_settlements.len(), 1);
        assert_eq!(result.funding_settlements[0].payment_quote_lots, "-77");
        assert_eq!(result.funding_settlements[0].symbol, "SOL");
    }
    #[test]
    fn wrong_collector_or_owner_never_receives_fee_or_funding_attribution() {
        let mut q = fixture();
        q.builder_authority = Some(Pubkey::new_from_array([8; 32]).to_string());
        assert!(decode(q).unwrap().flight_fee_quote_lots.is_none());
        let mut q = fixture();
        q.owner_authority = Pubkey::new_from_array([9; 32]).to_string();
        let result = decode(q).unwrap();
        assert!(result.flight_fee_quote_lots.is_none());
        assert!(result.funding_settlements.is_empty());
    }
    #[test]
    fn partial_unknown_and_foreign_log_evidence_fail_closed() {
        let mut q = fixture();
        let ix = &mut q.inner_instructions[0].instructions[1];
        let mut bytes = bs58::decode(&ix.data).into_vec().unwrap();
        bytes.pop();
        ix.data = bs58::encode(bytes).into_string();
        assert!(decode(q).is_err());
        let mut q = fixture();
        q.inner_instructions[0].instructions.pop();
        assert!(decode(q).is_err());
        let mut q = fixture();
        q.inner_instructions[0].index = 1;
        assert!(decode(q).is_err());
        let mut q = fixture();
        q.inner_instructions[0].instructions[1].program_id_index = 0;
        assert!(decode(q).is_err());
        let mut q = fixture();
        q.expected_message_base64 = STANDARD.encode([0; 10]);
        assert!(decode(q).is_err());
    }
}
