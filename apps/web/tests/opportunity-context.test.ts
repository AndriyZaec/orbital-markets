import assert from 'node:assert/strict'
import test from 'node:test'

import { findPositionOpportunity } from '../src/lib/opportunity-context.ts'

const opportunities = [
  {
    id: 'SOL-pacifica-hyperliquid-long-b',
    asset: 'SOL',
    venue_pair: { venue_a: 'pacifica', venue_b: 'hyperliquid' },
  },
  {
    id: 'BTC-pacifica-hyperliquid-long-a',
    asset: 'BTC',
    venue_pair: { venue_a: 'pacifica', venue_b: 'hyperliquid' },
  },
]

test('uses the exact opportunity that opened a position when it is still current', () => {
  const result = findPositionOpportunity(opportunities, {
    opportunity_id: opportunities[1].id,
    asset: 'BTC',
    venue_a: 'pacifica',
    venue_b: 'hyperliquid',
  })

  assert.equal(result?.id, opportunities[1].id)
})

test('falls back to current asset and venue context after a direction change', () => {
  const result = findPositionOpportunity(opportunities, {
    opportunity_id: 'SOL-pacifica-hyperliquid-long-a',
    asset: 'SOL',
    venue_a: 'hyperliquid',
    venue_b: 'pacifica',
  })

  assert.equal(result?.id, opportunities[0].id)
})

test('returns no context instead of retaining an unrelated opportunity', () => {
  const result = findPositionOpportunity(opportunities, {
    opportunity_id: 'ETH-pacifica-hyperliquid-long-a',
    asset: 'ETH',
    venue_a: 'pacifica',
    venue_b: 'hyperliquid',
  })

  assert.equal(result, null)
})
