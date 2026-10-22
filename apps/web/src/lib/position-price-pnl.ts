import type { Venue } from '@/agents/types'

export interface PositionPricePnLPoint {
  t: string
  segment?: number
  long_pnl: number | null
  short_pnl: number | null
  price_pnl: number | null
}

export type PositionPricePnLContext = { available: false; reason: string } | {
  available: true
  long_venue: Venue
  short_venue: Venue
  opened_at: string
  valuation_key: string
  latest: PositionPricePnLPoint
}

// Keep the monitor snapshot authoritative. Never interpolate an entry zero or
// let a later historical request replace the panel's current valuation.
export function positionPricePnLSeries(history: PositionPricePnLPoint[], latest: PositionPricePnLPoint, openedAt: string, windowStart?: string) {
  const latestTime = Date.parse(latest.t)
  const cutoff = Math.max(Date.parse(openedAt), latestTime - 86_400_000, windowStart ? Date.parse(windowStart) : -Infinity)
  const points = history.filter((point) => {
    const time = Date.parse(point.t)
    return time >= cutoff && time < latestTime
  }).toSorted((a, b) => Date.parse(a.t) - Date.parse(b.t))
  if (latestTime >= cutoff) {
    const segment = points[points.length - 1]?.segment
    points.push(segment === undefined ? latest : { ...latest, segment })
  }
  return points.filter((point, index) => Date.parse(point.t) !== Date.parse(points[index + 1]?.t ?? ''))
}
