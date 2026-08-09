import assert from 'node:assert/strict'
import test from 'node:test'

import { runSingleFlight } from '../src/lib/polling.ts'

test('runSingleFlight skips overlapping polls and allows the next poll after completion', async () => {
  let calls = 0
  let release: (() => void) | undefined
  const state = { running: false }
  const poll = () => runSingleFlight(state, async () => {
    calls++
    await new Promise<void>((resolve) => { release = resolve })
  })

  const first = poll()
  await poll()
  assert.equal(calls, 1)

  release?.()
  await first

  const third = poll()
  assert.equal(calls, 2)
  release?.()
  await third
})
