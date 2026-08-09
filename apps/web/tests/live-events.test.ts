import assert from 'node:assert/strict'
import test from 'node:test'

import { hasActiveLiveExposure, subscribeLiveAccountEvents } from '../src/lib/live-events.ts'

test('detects persisted live exposure that requires background monitoring', () => {
  assert.equal(hasActiveLiveExposure([{ state: 'closed' }]), false)
  assert.equal(hasActiveLiveExposure([{ state: 'open' }]), true)
  assert.equal(hasActiveLiveExposure([{ state: 'degraded' }]), true)
  assert.equal(hasActiveLiveExposure([{ state: 'closing' }]), true)
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
    const unsubscribeBalances = subscribeLiveAccountEvents('sol-wallet', '0xWallet', () => {})
    sources[0].onopen?.()
    const unsubscribePositions = subscribeLiveAccountEvents(
      'sol-wallet',
      '0xwallet',
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
