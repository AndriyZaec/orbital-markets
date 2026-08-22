import assert from 'node:assert/strict'
import test from 'node:test'

import { waitForClosedPosition } from '../src/lib/live-close.ts'

test('close confirmation tolerates a transient degraded state before reconciliation closes the position', async () => {
  const states = ['degraded', 'closed']
  let reads = 0

  await waitForClosedPosition({
    getPositionState: async () => states[reads++] ?? 'closed',
    delay: async () => {},
    attempts: 3,
    pollMs: 0,
  })

  assert.equal(reads, 2)
})

test('close confirmation still reports degraded exposure after the polling window', async () => {
  let reads = 0

  await assert.rejects(
    waitForClosedPosition({
      getPositionState: async () => {
        reads++
        return 'degraded'
      },
      delay: async () => {},
      attempts: 3,
      pollMs: 0,
    }),
    /manual action may be required/,
  )

  assert.equal(reads, 3)
})
