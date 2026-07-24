import assert from 'node:assert/strict'
import test from 'node:test'
import {
  annualizeFunding,
  calculateFundingStats,
  calculateReturnProjection,
  directionalCarry,
  paddedChartDomain,
  type FundingSample,
} from '../src/lib/funding-chart.ts'

const samples: FundingSample[] = [
  { t: '2026-07-22T10:00:00Z', funding_a: 0.0001, funding_b: 0.0003 },
  { t: '2026-07-22T11:00:00Z', funding_a: 0.0002, funding_b: 0.0003 },
  { t: '2026-07-22T12:00:00Z', funding_a: 0.0004, funding_b: 0.0003 },
]

test('annualizes normalized hourly funding rates', () => {
  assert.equal(annualizeFunding(0.0001), 0.876)
})

test('calculates carry using the selected long and short legs', () => {
  assert.ok(Math.abs(directionalCarry(samples[0], 'long_a_short_b') - 0.0002) < 1e-12)
  assert.ok(Math.abs(directionalCarry(samples[0], 'long_b_short_a') + 0.0002) < 1e-12)
})

test('summarizes signed carry rather than absolute edge', () => {
  const stats = calculateFundingStats(samples, 'long_a_short_b')

  assert.ok(stats)
  assert.ok(Math.abs(stats.averageCarry - 0.584) < 1e-12)
  assert.ok(Math.abs(stats.currentCarry + 0.876) < 1e-12)
  assert.equal(stats.positiveShare, 2 / 3)
})

test('projects return after costs and exposes break-even time', () => {
  const projection = calculateReturnProjection(samples.slice(0, 2), 'long_a_short_b', 10_000, 0.001, 24)

  assert.ok(projection)
  assert.equal(projection.costs, 10)
  assert.ok(Math.abs(projection.potentialReturn - 26) < 1e-9)
  assert.ok(Math.abs((projection.breakEvenHours ?? 0) - 6.666666666666667) < 1e-9)
  assert.ok(projection.lowerCarryPerHour <= projection.baseCarryPerHour)
  assert.ok(projection.upperCarryPerHour >= projection.baseCarryPerHour)
})

test('does not claim break-even when average carry is non-positive', () => {
  const projection = calculateReturnProjection(samples, 'long_b_short_a', 10_000, 0.001, 24)

  assert.ok(projection)
  assert.equal(projection.breakEvenHours, null)
  assert.ok(projection.potentialReturn < 0)
})

test('keeps cent-sized return projections readable', () => {
  const domain = paddedChartDomain([0, 0.04], 0.02)

  assert.ok(domain.min < 0)
  assert.ok(domain.max > 0.04)
  assert.ok(domain.max - domain.min < 0.1)
})

test('gives a stable domain to a flat zero-cost projection', () => {
  assert.deepEqual(paddedChartDomain([0, 0], 0.02), { min: -0.0124, max: 0.0124 })
})
