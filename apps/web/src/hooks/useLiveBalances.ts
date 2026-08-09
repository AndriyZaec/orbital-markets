import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch } from '@/lib/api'
import { runSingleFlight } from '@/lib/polling'

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
export function useLiveBalances(
  accountPacifica: string | null,
  accountHyperliquid: string | null,
  pollInterval = 30_000,
) {
  const pair = accountPacifica && accountHyperliquid
    ? `${accountPacifica}|${accountHyperliquid.toLowerCase()}`
    : null
  const [result, setResult] = useState<{ pair: string | null; balances: Balances }>({
    pair: null,
    balances: EMPTY,
  })
  const polling = useRef({ running: false })

  const fetch_ = useCallback(async () => {
    if (!pair || !accountPacifica || !accountHyperliquid) return
    await runSingleFlight(polling.current, async () => {
      try {
        const query = new URLSearchParams({
          account_pacifica: accountPacifica,
          account_hyperliquid: accountHyperliquid,
        })
        const resp = await apiFetch(`/api/v1/live/balances?${query}`)
        if (!resp.ok) return
        const data: Balances = await resp.json()
        setResult({ pair, balances: data })
      } catch {
        // silently ignore — balance display is best-effort
      }
    })
  }, [pair, accountPacifica, accountHyperliquid])

  useEffect(() => {
    const initialId = setTimeout(fetch_, 0)
    const intervalId = setInterval(fetch_, pollInterval)
    return () => {
      clearTimeout(initialId)
      clearInterval(intervalId)
    }
  }, [fetch_, pollInterval])

  // Expose refetch so ensure-account-streams callers can force a poll and
  // move the UI to "ready" without waiting for the next 5s tick. Returned
  // shape is a superset of Balances (adds `refetch`); existing consumers
  // that only read pacifica/hyperliquid are unaffected.
  const balances = result.pair === pair ? result.balances : EMPTY
  return { ...balances, refetch: fetch_ }
}
