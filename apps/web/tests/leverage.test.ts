import assert from 'node:assert/strict'
import test from 'node:test'

import { knownMaxLeverage, leverageCapabilityMessage, reconcileLeverageSelection } from '../src/lib/leverage.ts'

test('does not present unavailable Aster reference leverage as a missing market bracket', () => {
  assert.equal(leverageCapabilityMessage({
    aster: { status: 'unsupported', reason: 'reference_unavailable' },
  }), null)
})

test('does not present missing brackets for any venue', () => {
  for (const venue of ['hyperliquid', 'pacifica', 'aster']) {
    assert.equal(leverageCapabilityMessage({ [venue]: { status: 'missing' } }), null)
  }
})

test('keeps internal pending and stale capability state out of the UI', () => {
  assert.equal(leverageCapabilityMessage({
    hyperliquid: { status: 'pending', reason: 'account_pending' },
    aster: { status: 'stale', reason: 'target_refresh_failed' },
  }), null)
})

test('skips silent capabilities and returns a later actionable error', () => {
  assert.equal(leverageCapabilityMessage({
    hyperliquid: { status: 'missing' },
    pacifica: { status: 'out_of_range' },
  }), 'Position size is outside Pacifica leverage brackets')
  assert.equal(leverageCapabilityMessage({
    aster: { status: 'out_of_range', reason: 'invalid_notional' },
  }), 'Enter a valid position size')
})

test('treats missing leverage metadata as unknown', () => {
  assert.equal(knownMaxLeverage(0), null)
  assert.equal(knownMaxLeverage(undefined), null)
  assert.equal(knownMaxLeverage(10), 10)
})

test('selects an authoritative cap when an unknown cap becomes available', () => {
  assert.deepEqual(
    reconcileLeverageSelection({ opportunityId: 'meme', value: 1 }, 'meme', null, 20),
    { opportunityId: 'meme', value: 20 },
  )
})

test('clamps selection when the authoritative cap falls', () => {
  assert.deepEqual(
    reconcileLeverageSelection({ opportunityId: 'meme', value: 20 }, 'meme', 20, 8),
    { opportunityId: 'meme', value: 8 },
  )
})

test('does not carry a previous opportunity selection into a new cap', () => {
  assert.deepEqual(
    reconcileLeverageSelection({ opportunityId: 'btc', value: 5 }, 'meme', null, 12),
    { opportunityId: 'meme', value: 12 },
  )
})
