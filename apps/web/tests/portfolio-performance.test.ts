import assert from 'node:assert/strict'
import test from 'node:test'

import { portfolioPerformance } from '../src/lib/portfolio-performance.ts'

const year = 365 * 24 * 60 * 60 * 1000
const now = Date.parse('2026-08-31T00:00:00Z')

test('annualizes PnL against margin deployed on both legs', () => {
  const result = portfolioPerformance([{
    asset: 'BTC',
    notional: 100,
    leverage: 5,
    total_pnl: 0.4,
    started_at: new Date(now - year / 10).toISOString(),
  }], now)

  // $40 deployed for 10% of a year is four capital-years in dollar terms.
  assert.equal(result.value, 0.1)
  assert.equal(result.annualized, true)
})

test("weights portfolio return by each position's deployed capital and duration", () => {
  const result = portfolioPerformance([
    {
      asset: 'BTC',
      notional: 100,
      leverage: 5,
      total_pnl: 4,
      started_at: new Date(now - year).toISOString(),
      completed_at: new Date(now).toISOString(),
    },
    {
      asset: 'SOL',
      notional: 50,
      leverage: 5,
      total_pnl: 1,
      started_at: new Date(now - year / 2).toISOString(),
    },
  ], now)

  assert.equal(result.value, 0.1)
  assert.deepEqual(result.byAsset, [
    { asset: 'BTC', value: 0.1, annualized: true },
    { asset: 'SOL', value: 0.1, annualized: true },
  ])
})

test('shows a negative return on deployed capital without annualizing it', () => {
  const result = portfolioPerformance([{
    asset: 'BTC',
    notional: 100,
    leverage: 5,
    total_pnl: -0.4,
    started_at: new Date(now - year / 10).toISOString(),
  }], now)

  assert.equal(result.value, -0.01)
  assert.equal(result.annualized, false)
})

test('ignores positions without valid capital-time', () => {
  const result = portfolioPerformance([{
    asset: 'BTC',
    notional: 100,
    leverage: 0,
    total_pnl: 1,
    started_at: new Date(now - 1_000).toISOString(),
  }], now)

  assert.equal(result.value, null)
  assert.deepEqual(result.byAsset, [])
})
