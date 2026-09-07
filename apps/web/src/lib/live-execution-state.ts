import type { SigningRequest } from '@/types/signing'

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

export function executionFailurePhase(exposurePossible: boolean) {
  return exposurePossible ? 'recovering' as const : 'failed' as const
}

type Leg1SigningRequest = Pick<SigningRequest, 'action' | 'reduce_only' | 'venue'>

export function areValidLeg1SigningRequests(
  requests: Leg1SigningRequest[],
  riskierVenue: string,
  hedgeVenue: string,
): boolean {
  const openRequests = requests.filter((request) => request.action === 'open')
  const unwindRequests = requests.filter((request) => request.action === 'unwind')
  if (openRequests.length !== 1 || unwindRequests.length !== 1 ||
    openRequests[0].venue !== riskierVenue || openRequests[0].reduce_only ||
    unwindRequests[0].venue !== riskierVenue || !unwindRequests[0].reduce_only) {
    return false
  }

  const planVenues = new Set([riskierVenue, hedgeVenue])
  const leverageVenues = new Set<string>()
  for (const request of requests) {
    if (request.action === 'open' || request.action === 'unwind') continue
    if (request.action !== 'update_leverage' || !planVenues.has(request.venue) || leverageVenues.has(request.venue)) {
      return false
    }
    leverageVenues.add(request.venue)
  }
  return requests.length === leverageVenues.size + 2
}
