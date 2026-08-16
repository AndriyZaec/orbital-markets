import assert from 'node:assert/strict'
import test from 'node:test'
import { formatSignedUsdPnL } from '../src/lib/pnl-format.ts'

test('formats PnL with an explicit sign before the currency symbol', () => {
  assert.equal(formatSignedUsdPnL(5), '+$5.00')
  assert.equal(formatSignedUsdPnL(-5), '-$5.00')
  assert.equal(formatSignedUsdPnL(0), '+$0.00')
})
