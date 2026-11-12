import type { VenueReadiness } from '../hooks/useVenueReadiness'

// Presentation only. Callers authorizing execution must use current readiness.
export function accountObservation(r: VenueReadiness) {
  if (!r.walletConnected) return undefined
  return r.balanceObservation ?? (r.balanceReady && r.equity !== null && r.available !== null
    ? { equity: r.equity, available: r.available, observed_at: r.lastUpdated ?? '' } : undefined)
}
