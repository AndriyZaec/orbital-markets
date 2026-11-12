import { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import { apiFetch } from '@/lib/api'
import { runSingleFlight } from '@/lib/polling'
import { subscribeLiveAccountUpdates } from '@/lib/live-events'
import { useLiveExecution } from './useLiveExecution'
import { usePageVisibility } from './usePageVisibility'
import { liveAccountsQuery } from '@/lib/live-bindings'
import { accountRead } from '@/lib/account-read'

export interface BalanceObservation {
  equity: number
  available: number
  observed_at: string
}

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
  unavailable?: boolean
  observation?: BalanceObservation
  phoenix?: { live_ready: boolean; live_unavailable_reason?: string; registered_agent?: string; action_verification_required?: boolean }
}

interface Balances {
  pacifica: VenueBalance
  hyperliquid: VenueBalance
  aster: VenueBalance
  phoenix: VenueBalance
}

const EMPTY: Balances = {
  pacifica: { venue: 'pacifica', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
  hyperliquid: { venue: 'hyperliquid', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
  aster: { venue: 'aster', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
  phoenix: { venue: 'phoenix', equity: 0, available: 0, connected: false, stream_ready: false, fresh: false, age_seconds: 0 },
}

// Balance display is background context. The real freshness gate lives in
// /live/prepare (30s admissionFreshness). One shared account SSE carries all
// venues; the 30s REST poll is a disconnected-stream fallback. Consumers also
// refresh on intent (opening trade panel / clicking Execute Live) via refetch().
export function useLiveBalances(
  accountPacifica: string | null,
  accountHyperliquid: string | null,
  accountAster: string | null,
  accountPhoenix: string | null,
  pollInterval = 30_000,
) {
  const hyperliquidOwner = accountHyperliquid?.toLowerCase() ?? null
  const asterOwner = accountAster?.toLowerCase() ?? null
  const accounts = useMemo(() => Object.fromEntries(Object.entries({
    pacifica: accountPacifica,
    hyperliquid: hyperliquidOwner,
    aster: asterOwner,
    phoenix: accountPhoenix,
  }).filter((entry): entry is [string, string] => !!entry[1])), [asterOwner, hyperliquidOwner, accountPacifica, accountPhoenix])
  const pair = Object.keys(accounts).length > 0 ? JSON.stringify(accounts) : null
  const [result, setResult] = useState<Record<string, { owner: string; balance: VenueBalance; expiresAt: number }>>({})
  const [clock, setClock] = useState(() => Date.now())
  const polling = useMemo(() => ({ accountKey: pair, running: false }), [pair])
  const currentOwners = useRef(accounts)
  useEffect(() => { currentOwners.current = accounts }, [accounts])
  const streamConnected = useRef(false)
  const requestSequence = useRef(0)
  const pageVisible = usePageVisibility()
  const { state: execution } = useLiveExecution()
  const executionUsesPair = pair !== null && Object.entries(execution.accounts).every(([venue, account]) => {
    const current = accounts[venue]
    return venue === 'pacifica' || venue === 'phoenix' ? current === account : current?.toLowerCase() === account.toLowerCase()
  })
  const executionActive = executionUsesPair &&
    execution.phase !== 'idle' && execution.phase !== 'failed' && execution.phase !== 'aborted'
  // Wallet identity owns the subscription, not page/panel visibility. A hidden
  // tab keeps its push feed; only fallback reads depend on activity.
  const shouldMonitor = pair !== null
  const merge = useCallback((data: Partial<Balances>) => {
    const receivedAt = Date.now()
    setClock(receivedAt)
    setResult((previous) => {
      const next = { ...previous }
      for (const [venue, incoming] of Object.entries(data)) {
        const owner = accounts[venue]
        if (!owner || !incoming || currentOwners.current[venue] !== owner) continue
        const prior = previous[venue]?.owner === owner ? previous[venue].balance : undefined
        // Legacy responses can still supply a successful observation. Retained
        // observations never restore current connected/fresh/readiness fields.
        const observation = incoming.observation ?? (incoming.last_updated && (incoming.stream_ready ?? incoming.connected)
          ? { equity: incoming.equity, available: incoming.available, observed_at: incoming.last_updated }
          : prior?.observation)
        const age = typeof incoming.age_seconds === 'number' && Number.isFinite(incoming.age_seconds) ? incoming.age_seconds : null
        const expiresAt = age !== null ? receivedAt + Math.max(0, 30 - Math.max(0, age)) * 1_000
          : incoming.last_updated ? Date.parse(incoming.last_updated) + 30_000 : receivedAt + 30_000
        next[venue] = { owner, balance: { ...incoming, observation }, expiresAt: Number.isFinite(expiresAt) ? expiresAt : receivedAt }
      }
      return next
    })
  }, [accounts])

  // Expire current readiness even when SSE and fallback HTTP are both down.
  // This timer changes no observation timestamp and performs no network read.
  useEffect(() => {
    const next = Object.entries(result).filter(([venue, value]) => accounts[venue] === value.owner && value.balance.fresh !== false)
      .map(([, value]) => value.expiresAt).filter((at) => at > clock)
    if (next.length === 0) return
    const timer = window.setTimeout(() => setClock(Date.now()), Math.max(0, Math.min(...next) - Date.now()) + 1)
    return () => window.clearTimeout(timer)
  }, [accounts, result, clock])

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    if (signal?.aborted) return
    if (!pair) return
    await runSingleFlight(polling, async () => {
      const request = ++requestSequence.current
      try {
        await accountRead(async (readSignal) => {
          const query = liveAccountsQuery(accounts)
          const resp = await apiFetch(`/api/v1/live/balances?${query}`, { signal: readSignal })
          if (!resp.ok) return
          const data = await resp.json() as Partial<Balances>
          if (readSignal.aborted || request !== requestSequence.current) return
          merge(data)
        }, signal)
      } catch {
        // silently ignore — balance display is best-effort
      }
    })
  }, [accounts, pair, polling, merge])

  useEffect(() => {
    streamConnected.current = false
    if (!shouldMonitor || !pair) return
    let active = true
    const unsubscribe = subscribeLiveAccountUpdates(accounts, (event) => {
      if (!active) return
      if (event.type === 'connected') streamConnected.current = true
      else if (event.type === 'disconnected') streamConnected.current = false
      else if (event.type === 'balances') {
        requestSequence.current++
        merge(event.data as Partial<Balances>)
      }
    })
    return () => { active = false; unsubscribe() }
  }, [accounts, shouldMonitor, pair, merge])

  useEffect(() => {
    if (!pageVisible && !executionActive) return
    const controller = new AbortController()
    const initialId = setTimeout(() => fetch_(controller.signal), 0)
    const intervalId = setInterval(() => {
      if (!streamConnected.current) void fetch_(controller.signal)
    }, pollInterval)
    return () => {
      controller.abort()
      clearTimeout(initialId)
      clearInterval(intervalId)
    }
  }, [fetch_, pollInterval, pageVisible, executionActive])

  // Expose refetch so ensure-account-streams callers can force a poll and
  // move the UI to "ready" without waiting for the next fallback poll. Returned
  // shape is a superset of Balances (adds `refetch`); existing consumers
  // that only read pacifica/hyperliquid are unaffected.
  const balances = Object.fromEntries(Object.entries(EMPTY).map(([venue, empty]) => [venue,
    accounts[venue] && result[venue]?.owner === accounts[venue]
      ? clock >= result[venue].expiresAt ? { ...result[venue].balance, fresh: false } : result[venue].balance
      : empty,
  ])) as unknown as Balances
  return { ...balances, refetch: fetch_ }
}
