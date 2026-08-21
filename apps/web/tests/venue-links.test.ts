import assert from 'node:assert/strict'
import test from 'node:test'

import { venueTradeUrl } from '../src/lib/venue-links.ts'

test('builds allowlisted direct venue trade links', () => {
  assert.equal(venueTradeUrl('hyperliquid', '2Z'), 'https://app.hyperliquid.xyz/trade/2Z')
  assert.equal(venueTradeUrl('pacifica', 'sol'), 'https://app.pacifica.fi/trade/SOL')
})

test('rejects unsupported venues and empty symbols', () => {
  assert.equal(venueTradeUrl('unknown', 'SOL'), null)
  assert.equal(venueTradeUrl('hyperliquid', ''), null)
})
