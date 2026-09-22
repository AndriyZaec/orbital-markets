import assert from 'node:assert/strict'
import test from 'node:test'

import { matchesVenueFilter } from '../src/lib/opportunity-filters.ts'

test('keeps an opportunity only when both venues are selected', () => {
  const pair = { venue_a: 'pacifica', venue_b: 'hyperliquid' }

  assert.equal(matchesVenueFilter(pair, ['pacifica', 'hyperliquid']), true)
  assert.equal(matchesVenueFilter(pair, ['pacifica', 'aster']), false)
})

test('matches venue names without case sensitivity', () => {
  const pair = { venue_a: 'Pacifica', venue_b: 'Hyperliquid' }

  assert.equal(matchesVenueFilter(pair, ['PACIFICA', 'HYPERLIQUID']), true)
})
