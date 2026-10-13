import assert from 'node:assert/strict'
import test from 'node:test'
import {
  assertExecutionIntentRequest,
  assertExecutionIntentRequests,
  assertPreparedExecutionIntent,
  areValidLeg1SigningRequests,
  executionIntentSide,
  executionFailurePhase,
  executionPhaseFromStatus,
  ExecutionIntentRequestError,
  executionGuardActionLabel,
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
  approvedBaseAmount: 1,
  maxSlippagePct: 0.005,
  expiresAt: '2026-09-08T12:01:00.000Z',
  legs: [
    { venue: 'aster', symbol: 'BTCUSDT', side: 'buy', expectedPrice: 100 },
    { venue: 'pacifica', symbol: 'BTC', side: 'sell', expectedPrice: 100 },
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

test('execution intent permits disclosed L2 price limits without widening the approved amount', () => {
  const depthIntent = { ...intent, impactAcknowledged: true, legs: [
    { ...intent.legs[0], limitPrice: 110.55 },
    { ...intent.legs[1], limitPrice: 89.55 },
  ] as const }
  const now = Date.parse('2026-09-08T12:00:00.000Z')
  assert.doesNotThrow(() => assertExecutionIntentRequest(depthIntent, signingRequest({ price: 110.55, limit_price: 110.55 }), new Set(), now))
  assert.throws(() => assertExecutionIntentRequest(depthIntent, signingRequest({ price: 120, limit_price: 120 }), new Set(), now), /allowed range/)
  assert.throws(() => assertExecutionIntentRequest(depthIntent, signingRequest({ price: 110.55, limit_price: 110.55, amount: 1.1 }), new Set(), now), /approved plan/)
  assert.throws(() => assertExecutionIntentRequest(intent, signingRequest({ limit_price: 110.55 }), new Set(), now), /allowed range/)
})

test('prepare cannot add another movement allowance to approved buy/sell limits', () => {
  const boundedIntent = { ...intent, legs: [
    { ...intent.legs[0], limitPrice: 100.5 },
    { ...intent.legs[1], venue: 'hyperliquid', limitPrice: 99.5 },
  ] as const }
  const now = Date.parse('2026-09-08T12:00:00.000Z')
  const buy = signingRequest({ price: 100.902, limit_price: 100.902 })
  const sell = signingRequest({ venue: 'hyperliquid', symbol: 'BTC', side: 'sell', price: 99.6, limit_price: 99.102 })
  assert.throws(() => assertExecutionIntentRequest(boundedIntent, buy, new Set(), now), /allowed range/)
  assert.throws(() => assertExecutionIntentRequest(boundedIntent, sell, new Set(), now), /allowed range/)
  assert.doesNotThrow(() => assertExecutionIntentRequest(boundedIntent, { ...buy, price: 100.5, limit_price: 100.5 }, new Set(), now))
  assert.doesNotThrow(() => assertExecutionIntentRequest(boundedIntent, { ...sell, limit_price: 99.5 }, new Set(), now))
  assert.doesNotThrow(() => assertExecutionIntentRequest(boundedIntent, { ...sell, price: 99.5, limit_price: 99.5 }, new Set(), now))
  // Venue rounding that tightens the bound is valid; genuine one-tick widening is not.
  assert.doesNotThrow(() => assertExecutionIntentRequest(boundedIntent, { ...buy, price: 100.4, limit_price: 100.4 }, new Set(), now))
  assert.throws(() => assertExecutionIntentRequest(boundedIntent, { ...buy, price: 100.501, limit_price: 100.501 }, new Set(), now), /allowed range/)
})

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

test('execution intent bounds order amount and price before consuming the batch', () => {
  const now = Date.parse('2026-09-08T12:00:00.000Z')
  const consumed = new Set<string>()
  const valid = signingRequest({ id: 'valid', price: 100.5, amount: 1.005 })
  assert.doesNotThrow(() => assertExecutionIntentRequests(intent, [valid], consumed, now))
  assert.deepEqual([...consumed], ['valid'])

  const oversized = signingRequest({ id: 'oversized', amount: 1.01 })
  const batchConsumed = new Set<string>()
  assert.throws(
    () => assertExecutionIntentRequests(intent, [signingRequest({ id: 'leverage', action: 'update_leverage', side: '', amount: 0, price: 0, leverage: 3 }), oversized], batchConsumed, now),
    (error) => error instanceof ExecutionIntentRequestError && error.kind === 'plan_mismatch',
  )
  assert.equal(batchConsumed.size, 0)

  assert.throws(
    () => assertExecutionIntentRequest(intent, signingRequest({ id: 'moved', price: 101.01 }), new Set(), now),
    (error) => error instanceof ExecutionIntentRequestError && error.kind === 'price_moved',
  )
  assert.equal(executionGuardActionLabel('price_moved'), 'Refresh quote & retry')
  assert.equal(executionGuardActionLabel('plan_mismatch'), 'Refresh plan & retry')
})

test('execution intent accepts Pacifica native bid and ask sides', () => {
  const pacificaIntent = {
    ...intent,
    legs: [
      { venue: 'pacifica', symbol: '2Z', side: 'sell', expectedPrice: 100 },
      { venue: 'aster', symbol: '2ZUSDT', side: 'buy', expectedPrice: 100 },
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

test('execution intent applies the same bounds to Hyperliquid orders', () => {
  const hyperliquidIntent = {
    ...intent,
    legs: [
      { venue: 'hyperliquid', symbol: 'BTC', side: 'buy', expectedPrice: 100 },
      intent.legs[1],
    ],
  } as const
  const now = Date.parse('2026-09-08T12:00:00.000Z')

  assert.doesNotThrow(() => assertExecutionIntentRequest(
    hyperliquidIntent,
    signingRequest({ id: 'hl-valid', venue: 'hyperliquid', symbol: 'BTC', price: 100.5 }),
    new Set(),
    now,
  ))
  assert.throws(() => assertExecutionIntentRequest(
    hyperliquidIntent,
    signingRequest({ id: 'hl-oversized', venue: 'hyperliquid', symbol: 'BTC', amount: 1.01 }),
    new Set(),
    now,
  ), /approved plan/)
})

test('execution intent allows the common hedge amount across different plan prices', () => {
  const spreadIntent = {
    ...intent,
    approvedBaseAmount: 100 / 99,
    legs: [
      { venue: 'aster', symbol: 'BTCUSDT', side: 'buy', expectedPrice: 100 },
      { venue: 'pacifica', symbol: 'BTC', side: 'sell', expectedPrice: 99 },
    ],
  } as const

  assert.doesNotThrow(() => assertExecutionIntentRequest(
    spreadIntent,
    signingRequest({ id: 'common-hedge-size', venue: 'aster', amount: 100 / 99, price: 100.5 }),
    new Set(),
    Date.parse('2026-09-08T12:00:00.000Z'),
  ))
})
