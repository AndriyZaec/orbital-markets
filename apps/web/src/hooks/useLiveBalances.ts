import { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import { apiFetch } from '@/lib/api'
import { runSingleFlight, shouldMonitorLiveUpdates } from '@/lib/polling'
import { hasActiveLiveExposure, subscribeLiveAccountEvents } from '@/lib/live-events'
import { useLiveExecution } from './useLiveExecution'
import { usePageVisibility } from './usePageVisibility'
import { liveAccountsQuery } from '@/lib/live-bindings'

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
  aster: VenueBalance
}

const EMPTY: Balances = {
  pacifica: { venue: 'pacifica', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
  hyperliquid: { venue: 'hyperliquid', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
  aster: { venue: 'aster', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
}

// Balance display is background context. The real freshness gate lives in
// /live/prepare (30s admissionFreshness). 30s poll here keeps request volume
// low while still catching a broken stream well within the 5-minute display
// staleness window. Consumers refresh on intent (opening trade panel /
// clicking Execute Live) via balances.refetch().
export function useLiveBalances(
  accountPacifica: string | null,
  accountHyperliquid: string | null,
  accountAster: string | null,
  pollInterval = 30_000,
) {
  const accounts = useMemo(() => Object.fromEntries(Object.entries({
    pacifica: accountPacifica,
    hyperliquid: accountHyperliquid,
    aster: accountAster,
  }).filter((entry): entry is [string, string] => !!entry[1])), [accountAster, accountHyperliquid, accountPacifica])
  const pair = Object.keys(accounts).length > 0 ? JSON.stringify(accounts) : null
  const [result, setResult] = useState<{ pair: string | null; balances: Balances }>({
    pair: null,
    balances: EMPTY,
  })
  const polling = useRef({ running: false })
  const streamConnected = useRef(false)
  const requestSequence = useRef(0)
  const pageVisible = usePageVisibility()
  const { state: execution } = useLiveExecution()
  const [exposure, setExposure] = useState<{
    pair: string | null
    active: boolean | null
  }>({ pair: null, active: null })
  const activeExposure = exposure.pair === pair ? exposure.active : null
  const executionUsesPair = pair !== null && Object.entries(execution.accounts).every(([venue, account]) => {
    const current = accounts[venue]
    return venue === 'pacifica' ? current === account : current?.toLowerCase() === account.toLowerCase()
  })
  const executionActive = executionUsesPair &&
    execution.phase !== 'idle' && execution.phase !== 'failed' && execution.phase !== 'aborted'
  const shouldMonitor = shouldMonitorLiveUpdates(pageVisible, activeExposure, executionActive)

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    if (signal?.aborted) return
    if (!pair) return
    await runSingleFlight(polling.current, async () => {
      const request = ++requestSequence.current
      try {
        const query = liveAccountsQuery(accounts)
        const resp = await apiFetch(`/api/v1/live/balances?${query}`, { signal })
        if (!resp.ok) return
        const data = await resp.json() as Partial<Balances>
        if (signal?.aborted || request !== requestSequence.current) return
        setResult({ pair, balances: { ...EMPTY, ...data } })
      } catch {
        // silently ignore — balance display is best-effort
      }
    })
  }, [accounts, pair])

  useEffect(() => {
    streamConnected.current = false
    if (!shouldMonitor || !pair || !accountPacifica || !accountHyperliquid) return
    return subscribeLiveAccountEvents({
      pacifica: accountPacifica,
      hyperliquid: accountHyperliquid,
    }, (event) => {
      if (event.type === 'connected') streamConnected.current = true
      else if (event.type === 'disconnected') streamConnected.current = false
      else if (event.type === 'balances') {
        requestSequence.current++
        setResult((current) => ({
          pair,
          balances: { ...(current.pair === pair ? current.balances : EMPTY), ...(event.data as Partial<Balances>) },
        }))
      } else if (event.type === 'positions') {
        setExposure({ pair, active: hasActiveLiveExposure(event.data) })
      }
    })
  }, [shouldMonitor, pair, accountPacifica, accountHyperliquid])

  useEffect(() => {
    if (!pageVisible) return
    const controller = new AbortController()
    const initialId = setTimeout(() => fetch_(controller.signal), 0)
    const intervalId = setInterval(() => {
      if (!streamConnected.current || accountAster) void fetch_(controller.signal)
    }, accountAster ? 5_000 : pollInterval)
    return () => {
      controller.abort()
      clearTimeout(initialId)
      clearInterval(intervalId)
    }
  }, [accountAster, fetch_, pollInterval, pageVisible])

  // Expose refetch so ensure-account-streams callers can force a poll and
  // move the UI to "ready" without waiting for the next 5s tick. Returned
  // shape is a superset of Balances (adds `refetch`); existing consumers
  // that only read pacifica/hyperliquid are unaffected.
  const balances = result.pair === pair ? result.balances : EMPTY
  return { ...balances, refetch: fetch_ }
}
