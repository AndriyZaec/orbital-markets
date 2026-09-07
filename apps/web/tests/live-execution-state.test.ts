import assert from 'node:assert/strict'
import test from 'node:test'
import {
  areValidLeg1SigningRequests,
  executionFailurePhase,
  executionPhaseFromStatus,
  normalizeHyperliquidAddress,
  normalizePacificaAddress,
} from '../src/lib/live-execution-state.ts'

test('maps recovery statuses to explicit UI phases', () => {
  assert.equal(executionPhaseFromStatus('awaiting_leg2_retry_sign'), 'awaiting_leg2_retry')
  assert.equal(executionPhaseFromStatus('recovering'), 'recovering')
  assert.equal(executionPhaseFromStatus('degraded'), 'degraded')
})

test('routes failures after submission to recovery', () => {
  assert.equal(executionFailurePhase(false), 'failed')
  assert.equal(executionFailurePhase(true), 'recovering')
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
