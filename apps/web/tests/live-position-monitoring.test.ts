import assert from 'node:assert/strict'
import test from 'node:test'
import { monitoredLegVenues } from '../src/lib/live-position-monitoring.ts'

test('monitoring labels follow persisted execution leg order', () => {
  const venues = monitoredLegVenues([
    { leg: 1, venue: 'hyperliquid' },
    { leg: 2, venue: 'pacifica' },
  ], 'pacifica', 'hyperliquid')

  assert.deepEqual(venues, ['hyperliquid', 'pacifica'])
})

test('monitoring labels fall back to plan venues before fills load', () => {
  assert.deepEqual(monitoredLegVenues([], 'pacifica', 'hyperliquid'), ['pacifica', 'hyperliquid'])
})
