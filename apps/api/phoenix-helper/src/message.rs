use base64::Engine as _;
use base64::engine::general_purpose::STANDARD;
use serde::Serialize;
use solana_message::legacy::Message;

#[derive(Serialize)]
pub struct RequiredSigner {
    pub index: usize,
    pub pubkey: String,
    pub role: &'static str,
}

#[derive(Serialize)]
pub struct AccountManifest {
    pub index: usize,
    pub pubkey: String,
    pub signer: bool,
    pub writable: bool,
}

#[derive(Serialize)]
pub struct InstructionManifest {
    pub program_id: String,
    pub program_id_index: u8,
    pub account_indexes: Vec<u8>,
    pub data_base64: String,
}

pub fn describe(message: &Message) -> (Vec<AccountManifest>, Vec<InstructionManifest>) {
    let required_signatures = usize::from(message.header.num_required_signatures);
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
    (account_keys, instructions)
}
