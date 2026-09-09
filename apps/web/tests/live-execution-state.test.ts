import assert from 'node:assert/strict'
import test from 'node:test'
import {
  assertExecutionIntentRequest,
  assertPreparedExecutionIntent,
  areValidLeg1SigningRequests,
  executionIntentSide,
  executionFailurePhase,
  executionPhaseFromStatus,
  normalizeHyperliquidAddress,
  normalizePacificaAddress,
} from '../src/lib/live-execution-state.ts'
import type { SigningRequest } from '../src/types/signing.ts'

test('maps recovery statuses to explicit UI phases', () => {
  assert.equal(executionPhaseFromStatus('awaiting_leg2_retry_sign'), 'awaiting_leg2_retry')
  assert.equal(executionPhaseFromStatus('recovering'), 'recovering')
  assert.equal(executionPhaseFromStatus('degraded'), 'degraded')
})

test('routes failures after submission to recovery', () => {
  assert.equal(executionFailurePhase(false), 'failed')
  assert.equal(executionFailurePhase(true), 'recovering')
})

test('maps both plan directions to their order sides', () => {
  assert.equal(executionIntentSide('long'), 'buy')
  assert.equal(executionIntentSide('short'), 'sell')
})

test('normalizes wallet addresses using venue semantics', () => {
  assert.equal(normalizePacificaAddress('  SolCaseSensitive  '), 'SolCaseSensitive')
  assert.equal(normalizeHyperliquidAddress('  0xAbCd  '), '0xabcd')
  assert.equal(normalizePacificaAddress(null), null)
})

test('accepts optional per-venue leverage requests before leg 1', () => {
  const open = { action: 'open', venue: 'pacifica', reduce_only: false } as const
  const unwind = { action: 'unwind', venue: 'pacifica', reduce_only: true } as const
  const pacificaLeverage = { action: 'update_leverage', venue: 'pacifica', reduce_only: false } as const
  const hyperliquidLeverage = { action: 'update_leverage', venue: 'hyperliquid', reduce_only: false } as const

  assert.equal(areValidLeg1SigningRequests([open, unwind], 'pacifica', 'hyperliquid'), true)
  assert.equal(areValidLeg1SigningRequests([pacificaLeverage, open, unwind], 'pacifica', 'hyperliquid'), true)
  assert.equal(areValidLeg1SigningRequests([
    pacificaLeverage, hyperliquidLeverage, open, unwind,
  ], 'pacifica', 'hyperliquid'), true)
  assert.equal(areValidLeg1SigningRequests([
    pacificaLeverage, pacificaLeverage, open, unwind,
  ], 'pacifica', 'hyperliquid'), false)
})

const intent = {
  opportunityId: 'opp-1',
  asset: 'BTC',
  leverage: 3,
  requestedNotional: 100,
  expiresAt: '2026-09-08T12:01:00.000Z',
  legs: [
    { venue: 'aster', symbol: 'BTCUSDT', side: 'buy' },
    { venue: 'pacifica', symbol: 'BTC', side: 'sell' },
  ],
} as const

function signingRequest(overrides: Partial<SigningRequest> = {}): SigningRequest {
  return {
    id: 'request-1',
    client_order_id: 'client-1',
    venue: 'aster',
    action: 'open',
    account: 'owner',
    symbol: 'BTCUSDT',
    side: 'buy',
    amount: 1,
    price: 100,
    reduce_only: false,
    unsigned_payload: {},
    expires_at: '2026-09-08T12:00:30.000Z',
    created_at: '2026-09-08T12:00:00.000Z',
    ...overrides,
  }
}

test('execution intent accepts only the prepared asset and venue pair', () => {
  assert.doesNotThrow(() => assertPreparedExecutionIntent(intent, {
    asset: 'BTC',
    riskierVenue: 'aster',
    hedgeVenue: 'pacifica',
  }, Date.parse('2026-09-08T12:00:00.000Z')))

  assert.throws(() => assertPreparedExecutionIntent(intent, {
    asset: 'ETH',
    riskierVenue: 'aster',
    hedgeVenue: 'pacifica',
  }, Date.parse('2026-09-08T12:00:00.000Z')), /intent/)
  assert.throws(() => assertPreparedExecutionIntent(intent, {
    asset: 'BTC',
    riskierVenue: 'hyperliquid',
    hedgeVenue: 'pacifica',
  }, Date.parse('2026-09-08T12:00:00.000Z')), /intent/)
})

test('execution intent binds symbol, direction, leverage, lifetime, and reuse', () => {
  const consumed = new Set<string>()
  const now = Date.parse('2026-09-08T12:00:00.000Z')
  assert.doesNotThrow(() => assertExecutionIntentRequest(intent, signingRequest(), consumed, now))
  assert.throws(() => assertExecutionIntentRequest(intent, signingRequest(), consumed, now), /already consumed/)
  assert.throws(() => assertExecutionIntentRequest(intent, signingRequest({ id: 'wrong-side', side: 'sell' }), new Set(), now), /intent/)
  assert.throws(() => assertExecutionIntentRequest(intent, signingRequest({ id: 'wrong-symbol', symbol: 'ETHUSDT' }), new Set(), now), /intent/)
  assert.throws(() => assertExecutionIntentRequest(intent, signingRequest({
    id: 'wrong-leverage', action: 'update_leverage', side: '', amount: 0, price: 0, leverage: 4,
  }), new Set(), now), /intent/)
  assert.throws(() => assertExecutionIntentRequest(intent, signingRequest({ id: 'expired-intent' }), new Set(), Date.parse(intent.expiresAt) + 1), /expired/)

  assert.doesNotThrow(() => assertExecutionIntentRequest(intent, signingRequest({
    id: 'unwind', action: 'unwind', side: 'sell', reduce_only: true,
  }), new Set(), now))
})

test('execution intent accepts Pacifica native bid and ask sides', () => {
  const pacificaIntent = {
    ...intent,
    legs: [
      { venue: 'pacifica', symbol: '2Z', side: 'sell' },
      { venue: 'aster', symbol: '2ZUSDT', side: 'buy' },
    ],
  } as const
  const request = signingRequest({
    id: 'pacifica-short', venue: 'pacifica', symbol: '2Z', side: 'ask',
  })

  assert.doesNotThrow(() => assertExecutionIntentRequest(
    pacificaIntent, request, new Set(), Date.parse('2026-09-08T12:00:00.000Z'),
  ))
  assert.doesNotThrow(() => assertExecutionIntentRequest(
    pacificaIntent,
    { ...request, id: 'pacifica-unwind', action: 'unwind', side: 'bid', reduce_only: true },
    new Set(),
    Date.parse('2026-09-08T12:00:00.000Z'),
  ))
})
