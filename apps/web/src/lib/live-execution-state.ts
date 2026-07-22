export type AdvanceStatus =
  | 'awaiting_leg2_sign'
  | 'awaiting_leg2_retry_sign'
  | 'recovering'
  | 'open'
  | 'degraded'
  | 'aborted'
  | 'failed'

// Ethereum addresses are case-insensitive; Solana base58 remains case-sensitive.
export function normalizePacificaAddress(address: string | null): string | null {
  return address ? address.trim() : null
}

export function normalizeHyperliquidAddress(address: string | null): string | null {
  return address ? address.trim().toLowerCase() : null
}

export function executionPhaseFromStatus(status: AdvanceStatus) {
  switch (status) {
    case 'awaiting_leg2_sign': return 'awaiting_leg2' as const
    case 'awaiting_leg2_retry_sign': return 'awaiting_leg2_retry' as const
    default: return status
  }
}
