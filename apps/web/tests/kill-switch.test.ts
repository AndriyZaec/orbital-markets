import assert from 'node:assert/strict'
import test from 'node:test'

import { summarizeKillPreparation } from '../src/lib/kill-switch.ts'

test('fails closed when targeted positions produce no emergency close orders', () => {
  assert.deepEqual(summarizeKillPreparation(2, 0, []), {
    failed: 2,
    errors: ['No emergency close orders were prepared for the targeted positions'],
  })
})

test('preserves per-position preparation failures', () => {
  assert.deepEqual(summarizeKillPreparation(2, 1, [
    { id: 'position-1', legs_to_close: 2, error: 'account state unavailable' },
    { id: 'position-2', legs_to_close: 1 },
  ]), {
    failed: 2,
    errors: ['position-1: account state unavailable'],
  })
})
