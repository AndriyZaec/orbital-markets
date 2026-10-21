import { test } from 'node:test'
import assert from 'node:assert/strict'
import { positionPricePnLSeries, type PositionPricePnLPoint } from '../src/lib/position-price-pnl.ts'

function point(t: string, pnl: number | null): PositionPricePnLPoint {
  return { t, long_pnl: pnl, short_pnl: pnl === null ? null : 0, price_pnl: pnl }
}

test('uses the same current snapshot as the panel, replacing same-time and later historical values', () => {
  const latest = point('2026-10-21T12:00:00Z', -2)
  const raw = [point('2026-10-21T09:00:00Z', 999), point('2026-10-21T11:00:00Z', 3),
    point('2026-10-21T12:00:00Z', 4), point('2026-10-21T12:01:00Z', 100)]
  const result = positionPricePnLSeries(raw, latest, '2026-10-21T10:00:00Z')
  assert.deepEqual(result.map((p) => p.price_pnl), [3, -2])
  assert.equal(result[result.length - 1], latest)
  assert.equal(raw.length, 4)
})

test('retains since-entry valuation for older positions without inventing a zero at the 24h boundary', () => {
  const result = positionPricePnLSeries([
    point('2026-10-19T10:00:00Z', 1), point('2026-10-20T13:00:00Z', 7), point('2026-10-20T14:00:00Z', null),
  ], point('2026-10-21T12:00:00Z', 9), '2026-10-18T12:00:00Z')
  assert.deepEqual(result.map((p) => p.price_pnl), [7, null, 9])
})
