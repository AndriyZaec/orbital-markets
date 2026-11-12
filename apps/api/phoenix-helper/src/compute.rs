use serde::{Deserialize, Serialize};
use solana_compute_budget_interface::ComputeBudgetInstruction;
use solana_instruction::Instruction;
use solana_message::legacy::Message;

#[derive(Clone, Copy, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ComputeBudget {
    pub unit_limit: u32,
    pub unit_price_micro_lamports: u64,
}

impl Default for ComputeBudget {
    fn default() -> Self {
        Self {
            unit_limit: 400_000,
            unit_price_micro_lamports: 0,
        }
    }
}

impl ComputeBudget {
    pub fn instructions(self) -> Result<[Instruction; 2], String> {
        if self.unit_limit == 0
            || self.unit_limit > 1_400_000
            || self.unit_price_micro_lamports > 1_000_000
        {
            return Err("compute budget exceeds approved bounds".into());
        }
        Ok([
            ComputeBudgetInstruction::set_compute_unit_limit(self.unit_limit),
            ComputeBudgetInstruction::set_compute_unit_price(self.unit_price_micro_lamports),
        ])
    }
}

// Accounting also accepts historical single-order messages. New messages have
// exactly two canonical budget instructions followed by the order.
pub fn order_index(message: &Message) -> Result<usize, String> {
    if message.instructions.len() == 1 {
        return Ok(0);
    }
    if message.instructions.len() != 3 {
        return Err("unexpected accounting instructions".into());
    }
    let limit = &message.instructions[0];
    let price = &message.instructions[1];
    if limit.data.len() != 5 || price.data.len() != 9 {
        return Err("invalid compute instruction size".into());
    }
    let budget = ComputeBudget {
        unit_limit: u32::from_le_bytes(limit.data[1..].try_into().unwrap()),
        unit_price_micro_lamports: u64::from_le_bytes(price.data[1..].try_into().unwrap()),
    };
    for (actual, expected) in message.instructions[..2].iter().zip(budget.instructions()?) {
        if message
            .account_keys
            .get(usize::from(actual.program_id_index))
            != Some(&expected.program_id)
            || !actual.accounts.is_empty()
            || actual.data != expected.data
        {
            return Err("noncanonical compute instruction".into());
        }
    }
    Ok(2)
}
