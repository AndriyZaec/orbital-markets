import { test } from 'node:test'
import assert from 'node:assert/strict'
import { bucketSpreadSamples } from '../src/lib/price-spread-chart.ts'

test('two-minute buckets retain observed ranges, coherent median observations and latest values', () => {
  const samples = [
    { time: 0, value: 1, priceA: 100, priceB: 101 },
    { time: 20_000, value: 9, priceA: 100, priceB: 109 },
    { time: 40_000, value: 2, priceA: 100, priceB: 102 },
    { time: 60_000, value: 3, priceA: 100, priceB: 103 },
  ]
  const [segment] = bucketSpreadSamples(samples)
  assert.equal(segment.length, 2)
  assert.equal(segment[0].value, 1)
  assert.equal(segment[1].value, 3)
  assert.equal(segment[1].priceB, 103)
  assert.equal(segment[1].high, 9)
  assert.equal(segment[1].low, 1)
})

test('buckets never merge across missing quotes or long gaps', () => {
  const sample = { value: 1, priceA: 100, priceB: 101 }
  const segments = bucketSpreadSamples([
    { ...sample, time: 0 },
    { ...sample, value: null, time: 20_000 },
    { ...sample, time: 40_000 },
    { ...sample, time: 20 * 60_000 },
  ])
  assert.equal(segments.length, 3)
  assert.deepEqual(segments.map((segment) => segment[0].time), [0, 40_000, 20 * 60_000])
})

test('paired venue prices are selected from the same actual observation', () => {
  const samples = [
    { time: 0, value: 1, shortPrice: 10 },
    { time: 20_000, value: 3, shortPrice: 2 },
    { time: 40_000, value: 2, shortPrice: 4 },
  ]
  const [segment] = bucketSpreadSamples(samples)
  for (const point of segment) {
    const observed = samples.find((sample) => sample.time === point.time)
    assert.equal(point.value, observed?.value)
    assert.equal(point.shortPrice, observed?.shortPrice)
  }
})
