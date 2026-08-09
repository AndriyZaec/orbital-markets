import assert from 'node:assert/strict'
import test from 'node:test'

import {
  livePositionPollInterval,
  runSingleFlight,
  shouldMonitorLiveUpdates,
} from '../src/lib/polling.ts'

test('live position fallback polling slows down when there is no exposure', () => {
  assert.equal(livePositionPollInterval(false), 30_000)
  assert.equal(livePositionPollInterval(true), 5_000)
  assert.equal(livePositionPollInterval(null), 5_000)
})

test('hidden pages monitor only while exposure is active or still unknown', () => {
  assert.equal(shouldMonitorLiveUpdates(true, false), true)
  assert.equal(shouldMonitorLiveUpdates(false, true), true)
  assert.equal(shouldMonitorLiveUpdates(false, null), true)
  assert.equal(shouldMonitorLiveUpdates(false, false), false)
  assert.equal(shouldMonitorLiveUpdates(false, false, true), true)
})

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
