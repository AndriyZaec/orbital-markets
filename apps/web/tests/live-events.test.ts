import assert from 'node:assert/strict'
import test from 'node:test'

import {
  hasActiveLiveExposure,
  hasActiveLiveExposureForWallet,
  subscribeLiveAccountEvents,
} from '../src/lib/live-events.ts'

test('detects persisted live exposure that requires background monitoring', () => {
  assert.equal(hasActiveLiveExposure([{ state: 'closed' }]), false)
  assert.equal(hasActiveLiveExposure([{ state: 'pending' }]), true)
  assert.equal(hasActiveLiveExposure([{ state: 'open' }]), true)
  assert.equal(hasActiveLiveExposure([{ state: 'degraded' }]), true)
  assert.equal(hasActiveLiveExposure([{ state: 'closing' }]), true)
})

test('detects active exposure affected by the wallet being disconnected', () => {
  const positions = [
    { state: 'open', venue_a: 'pacifica', venue_b: 'hyperliquid' },
    { state: 'closed', venue_a: 'aster', venue_b: 'hyperliquid' },
  ]

  assert.equal(hasActiveLiveExposureForWallet(positions, 'solana'), true)
  assert.equal(hasActiveLiveExposureForWallet(positions, 'evm'), true)
  assert.equal(hasActiveLiveExposureForWallet([
    { state: 'open', venue_a: 'pacifica', venue_b: 'unknown' },
  ], 'evm'), false)
  assert.equal(hasActiveLiveExposureForWallet([
    { state: 'closed', venue_a: 'aster', venue_b: 'hyperliquid' },
  ], 'evm'), false)
})

test('account event subscribers share one EventSource per wallet pair', () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'EventSource')
  const sources: FakeEventSource[] = []

  class FakeEventSource {
    onopen: (() => void) | null = null
    onerror: (() => void) | null = null
    closed = false

    constructor() {
      sources.push(this)
    }

    addEventListener() {}
    close() { this.closed = true }
  }

  Object.defineProperty(globalThis, 'EventSource', { value: FakeEventSource, configurable: true })
  try {
    const lateEvents: string[] = []
    const unsubscribeBalances = subscribeLiveAccountEvents(
      { pacifica: 'sol-wallet', hyperliquid: '0xWallet' },
      () => {},
    )
    sources[0].onopen?.()
    const unsubscribePositions = subscribeLiveAccountEvents(
      { hyperliquid: '0xwallet', pacifica: 'sol-wallet' },
      (event) => lateEvents.push(event.type),
    )
    assert.equal(sources.length, 1)
    assert.deepEqual(lateEvents, ['connected'])

    unsubscribeBalances()
    assert.equal(sources[0].closed, false)
    unsubscribePositions()
    assert.equal(sources[0].closed, true)
  } finally {
    if (original) Object.defineProperty(globalThis, 'EventSource', original)
    else Reflect.deleteProperty(globalThis, 'EventSource')
  }
})
