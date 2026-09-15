import type { SigningRequest } from '@/types/signing'

export interface ExecutionIntent {
  opportunityId: string
  asset: string
  leverage: number
  requestedNotional: number
  approvedBaseAmount: number
  maxSlippagePct: number
  expiresAt: string
  legs: readonly [
    { venue: SigningRequest['venue']; symbol: string; side: 'buy' | 'sell'; expectedPrice: number },
    { venue: SigningRequest['venue']; symbol: string; side: 'buy' | 'sell'; expectedPrice: number },
  ]
}

export type ExecutionGuardFailure = 'price_moved' | 'plan_mismatch'

export class ExecutionIntentRequestError extends Error {
  readonly kind: ExecutionGuardFailure

  constructor(kind: ExecutionGuardFailure) {
    super(kind === 'price_moved'
      ? 'Market price moved beyond the allowed range.'
      : 'Order details no longer match the approved plan.')
    this.name = 'ExecutionIntentRequestError'
    this.kind = kind
  }
}

export function executionGuardActionLabel(kind: ExecutionGuardFailure): string {
  return kind === 'price_moved' ? 'Refresh quote & retry' : 'Refresh plan & retry'
}

export function executionIntentSide(positionSide: 'long' | 'short'): 'buy' | 'sell' {
  return positionSide === 'long' ? 'buy' : 'sell'
}

interface PreparedExecution {
  asset: string
  riskierVenue: string
  hedgeVenue: string
}

export function assertPreparedExecutionIntent(
  intent: ExecutionIntent,
  prepared: PreparedExecution,
  now = Date.now(),
): void {
  const expiresAt = Date.parse(intent.expiresAt)
  if (!Number.isFinite(expiresAt) || now > expiresAt) {
    throw new Error('Execution intent expired')
  }
  const venues = new Set(intent.legs.map((leg) => leg.venue))
  if (intent.legs[0].venue === intent.legs[1].venue ||
    prepared.asset.trim().toUpperCase() !== intent.asset.trim().toUpperCase() ||
    prepared.riskierVenue === prepared.hedgeVenue ||
    !venues.has(prepared.riskierVenue as SigningRequest['venue']) ||
    !venues.has(prepared.hedgeVenue as SigningRequest['venue'])) {
    throw new Error('Prepared execution does not match the execution intent')
  }
}

export function assertExecutionIntentRequest(
  intent: ExecutionIntent,
  request: SigningRequest,
  consumedRequestIds: Set<string>,
  now = Date.now(),
): void {
  assertExecutionIntentRequests(intent, [request], consumedRequestIds, now)
}

export function assertExecutionIntentRequests(
  intent: ExecutionIntent,
  requests: readonly SigningRequest[],
  consumedRequestIds: Set<string>,
  now = Date.now(),
): void {
  const batchIds = new Set<string>()
  for (const request of requests) {
    if (consumedRequestIds.has(request.id) || batchIds.has(request.id)) {
      throw new Error('Signing request was already consumed')
    }
    assertExecutionIntentRequestValues(intent, request, now)
    batchIds.add(request.id)
  }
  for (const id of batchIds) consumedRequestIds.add(id)
}

function assertExecutionIntentRequestValues(
  intent: ExecutionIntent,
  request: SigningRequest,
  now: number,
): void {
  const expiresAt = Date.parse(intent.expiresAt)
  if (!Number.isFinite(expiresAt) || now > expiresAt) throw new Error('Execution intent expired')

  const leg = intent.legs.find((candidate) => candidate.venue === request.venue)
  let valid = Boolean(leg) && request.symbol.trim().toUpperCase() === leg?.symbol.trim().toUpperCase()
  if (request.action === 'update_leverage') {
    valid = valid && request.leverage === intent.leverage
  } else if (request.action === 'open' || request.action === 'unwind') {
    const expectedSide = request.action === 'open'
      ? leg?.side
      : leg?.side === 'buy' ? 'sell' : 'buy'
    const expectedRequestSide = request.venue === 'pacifica'
      ? expectedSide === 'buy' ? 'bid' : 'ask'
      : expectedSide
    valid = valid && request.side === expectedRequestSide &&
      request.reduce_only === (request.action === 'unwind')
  } else {
    valid = false
  }
  if (!valid) throw new Error('Signing request does not match the execution intent')

  if (request.action !== 'open' && request.action !== 'unwind') return
  if (!leg || !Number.isFinite(intent.requestedNotional) || intent.requestedNotional <= 0 ||
    !Number.isFinite(intent.approvedBaseAmount) || intent.approvedBaseAmount <= 0 ||
    !Number.isFinite(intent.maxSlippagePct) || intent.maxSlippagePct < 0 || intent.maxSlippagePct > 0.005 ||
    intent.legs.some((candidate) => !Number.isFinite(candidate.expectedPrice) || candidate.expectedPrice <= 0) ||
    !Number.isFinite(request.amount) || request.amount <= 0 ||
    !Number.isFinite(request.price) || request.price <= 0) {
    throw new ExecutionIntentRequestError('plan_mismatch')
  }

  // The backend uses one base amount for both venues. Bind it to the riskier
  // leg shown in the approved plan, with room only for approved quote movement.
  const maximumAmount = intent.approvedBaseAmount / (1 - intent.maxSlippagePct)
  if (request.amount > maximumAmount) {
    throw new ExecutionIntentRequestError('plan_mismatch')
  }
  // Open builders use 0.5% venue slippage. A 1% browser ceiling leaves room for
  // venue tick normalization without allowing an unbounded API-provided price.
  if (request.action === 'open' && Math.abs(request.price / leg.expectedPrice - 1) > 0.01) {
    throw new ExecutionIntentRequestError('price_moved')
  }
}

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
