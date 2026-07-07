import { useEffect, useState, useCallback } from 'react'
import { apiFetch } from '@/lib/api'

interface VenueBalance {
  venue: string
  equity: number
  available: number
  connected: boolean
  // Backend-provided account-data readiness. stream_ready: subscriber
  // produced at least one snapshot. fresh: snapshot age within the freshness
  // threshold (see liveAccountFreshness on the backend). reason: human
  // explanation when not ready.
  stream_ready?: boolean
  fresh?: boolean
  last_updated?: string
  age_seconds?: number
  reason?: string
}

interface Balances {
  pacifica: VenueBalance
  hyperliquid: VenueBalance
}

const EMPTY: Balances = {
  pacifica: { venue: 'pacifica', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
  hyperliquid: { venue: 'hyperliquid', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
}

// Balance display is background context. The real freshness gate lives in
// /live/prepare (30s admissionFreshness). 30s poll here keeps request volume
// low while still catching a broken stream well within the 5-minute display
// staleness window. Consumers refresh on intent (opening trade panel /
// clicking Execute Live) via balances.refetch().
export function useLiveBalances(pollInterval = 30_000) {
  const [balances, setBalances] = useState<Balances>(EMPTY)

  const fetch_ = useCallback(async () => {
    try {
      const resp = await apiFetch('/api/v1/live/balances')
      if (!resp.ok) return
      const data: Balances = await resp.json()
      setBalances(data)
    } catch {
      // silently ignore — balance display is best-effort
    }
  }, [])

  useEffect(() => {
    fetch_()
    const id = setInterval(fetch_, pollInterval)
    return () => clearInterval(id)
  }, [fetch_, pollInterval])

  // Expose refetch so ensure-account-streams callers can force a poll and
  // move the UI to "ready" without waiting for the next 5s tick. Returned
  // shape is a superset of Balances (adds `refetch`); existing consumers
  // that only read pacifica/hyperliquid are unaffected.
  return { ...balances, refetch: fetch_ }
}
