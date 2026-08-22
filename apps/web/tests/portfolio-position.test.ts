import assert from 'node:assert/strict'
import test from 'node:test'

import { portfolioPositionCategory } from '../src/lib/portfolio-position.ts'

test('closed positions are not degraded by their historical hedge mismatch', () => {
  assert.equal(portfolioPositionCategory('closed', 1), 'closed')
})

test('hedge mismatch still degrades an active position', () => {
  assert.equal(portfolioPositionCategory('open', 0.02), 'degraded')
})
