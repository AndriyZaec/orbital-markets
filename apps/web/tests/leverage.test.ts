import assert from 'node:assert/strict'
import test from 'node:test'

import { knownMaxLeverage, reconcileLeverageSelection } from '../src/lib/leverage.ts'

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
