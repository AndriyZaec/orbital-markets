import assert from 'node:assert/strict'
import test from 'node:test'
import {
  executionPhaseFromStatus,
  normalizeHyperliquidAddress,
  normalizePacificaAddress,
} from '../src/lib/live-execution-state.ts'

test('maps recovery statuses to explicit UI phases', () => {
  assert.equal(executionPhaseFromStatus('awaiting_leg2_retry_sign'), 'awaiting_leg2_retry')
  assert.equal(executionPhaseFromStatus('recovering'), 'recovering')
  assert.equal(executionPhaseFromStatus('degraded'), 'degraded')
})

test('normalizes wallet addresses using venue semantics', () => {
  assert.equal(normalizePacificaAddress('  SolCaseSensitive  '), 'SolCaseSensitive')
  assert.equal(normalizeHyperliquidAddress('  0xAbCd  '), '0xabcd')
  assert.equal(normalizePacificaAddress(null), null)
})
