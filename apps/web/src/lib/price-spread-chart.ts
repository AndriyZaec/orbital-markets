export interface SpreadSample {
  time: number
  value: number | null
}

export type SpreadBucket<T extends SpreadSample = SpreadSample> = T & { value: number; low: number; high: number }

// Use an actual median observation per two-minute interval, keeping its prices
// and timestamp together. The range retains intrabucket extremes; endpoints
// retain the first/latest observation. Never aggregate across a missing quote.
export function bucketSpreadSamples<T extends SpreadSample>(samples: T[]): SpreadBucket<T>[][] {
  const segments: SpreadBucket<T>[][] = []
  let segment: SpreadBucket<T>[] = []
  let bucket: (T & { value: number })[] = []
  let first: SpreadBucket<T> | null = null
  let last: SpreadBucket<T> | null = null
  const flushBucket = () => {
    if (bucket.length === 0) return
    const ordered = bucket.toSorted((a, b) => a.value - b.value)
    segment.push({ ...ordered[Math.floor(ordered.length / 2)], low: ordered[0].value, high: ordered[ordered.length - 1].value })
    bucket = []
  }
  const flushSegment = () => {
    flushBucket()
    if (first && last && segment.length > 0) {
      if (first.time < segment[0].time) segment.unshift(first)
      if (last.time > segment[segment.length - 1].time) segment.push(last)
      segments.push(segment)
    }
    segment = []
    first = last = null
  }
  for (const sample of samples) {
    if (sample.value === null) {
      flushSegment()
      continue
    }
    if (last && sample.time - last.time > 15 * 60_000) flushSegment()
    if (bucket.length > 0 && Math.floor(sample.time / 120_000) !== Math.floor(bucket[0].time / 120_000)) flushBucket()
    bucket.push({ ...sample, value: sample.value })
    const point = { ...sample, value: sample.value, low: sample.value, high: sample.value }
    first ??= point
    last = point
  }
  flushSegment()
  return segments
}
