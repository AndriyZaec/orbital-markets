import { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import {
  livePositionPollInterval,
  runSingleFlight,
  shouldMonitorLiveUpdates,
} from '@/lib/polling'
import { hasActiveLiveExposure, subscribeLiveAccountEvents } from '@/lib/live-events'
import { useVenueAuthority } from './useVenueAuthority'
import { usePageVisibility } from './usePageVisibility'
import { liveAccountsKey, liveAccountsQuery } from '@/lib/live-bindings'
import type { Venue } from '@/agents/types'

export interface LivePosition {
  id: string
  plan_id: string
  opportunity_id: string
  asset: string
  venue_a: Venue
  venue_b: Venue
  state: string
  notional: number
  leverage: number
  entry_spread: number
  hedge_mismatch: number
  current_spread: number
  current_basis: number
  entry_basis: number
  basis_change: number
  price_pnl: number
  funding_pnl: number
  funding_pnl_source: 'pending' | 'estimated' | 'realized'
  total_pnl: number
  leg1_current_price: number
  leg2_current_price: number
  leg1_liq_price: number
  leg2_liq_price: number
  leg1_liq_dist: number
  leg2_liq_dist: number
  leg1_liq_risk: string
  leg2_liq_risk: string
  hold_hours: number
  started_at: string
  opened_at?: string
  completed_at?: string
  monitor_at?: string
  updated_at: string
}

export function useLivePositions(pollInterval = 5_000) {
  const [positions, setPositions] = useState<LivePosition[]>([])
  const [loadedAccountKey, setLoadedAccountKey] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const positionsRef = useRef<LivePosition[]>([])
  const positionsAccountKey = useRef('')
  const requestSequence = useRef(0)
  const polling = useRef({ running: false })
  const refreshQueued = useRef(false)
  const fetchRef = useRef<(signal?: AbortSignal) => Promise<void>>(async () => {})
  const streamConnected = useRef(false)
  const hasActivePositions = useRef<boolean | null>(null)
  const reschedulePolling = useRef<() => void>(() => {})
  const pageVisible = usePageVisibility()
  const { pacificaAddress, hyperliquidAddress, asterAddress } = useVenueAuthority()
  const accountPairs = useMemo(() => {
    const entries = Object.entries({
      pacifica: pacificaAddress,
      hyperliquid: hyperliquidAddress,
      aster: asterAddress,
    }).filter((entry): entry is [string, string] => !!entry[1])
    const pairs: Record<string, string>[] = []
    for (let left = 0; left < entries.length; left++) {
      for (let right = left + 1; right < entries.length; right++) {
        pairs.push(Object.fromEntries([entries[left], entries[right]]))
      }
    }
    return pairs
  }, [asterAddress, hyperliquidAddress, pacificaAddress])
  const accountKey = accountPairs.length > 0 ? JSON.stringify(accountPairs) : ''
  const [exposure, setExposure] = useState<{ accountKey: string; active: boolean | null }>({
    accountKey: '',
    active: null,
  })
  const activeExposure = exposure.accountKey === accountKey ? exposure.active : null
  const shouldMonitor = shouldMonitorLiveUpdates(pageVisible, activeExposure)

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    if (signal?.aborted) return
    if (polling.current.running) {
      refreshQueued.current = true
      return
    }
    refreshQueued.current = false
    if (accountPairs.length === 0) {
      positionsRef.current = []
      positionsAccountKey.current = ''
      setPositions([])
      setLoadedAccountKey('')
      setLoading(false)
      setError(null)
      return
    }
    await runSingleFlight(polling.current, async () => {
      const request = ++requestSequence.current
      try {
        const responses = await Promise.allSettled(accountPairs.map(async (accounts) => {
          const resp = await apiFetch(`/api/v1/live/positions?${liveAccountsQuery(accounts)}`, { signal })
          if (!resp.ok) throw await apiResponseError(resp, 'Unable to load live positions. Please try again.')
          return resp.json() as Promise<LivePosition[]>
        }))
        const fulfilled = responses
          .filter((result): result is PromiseFulfilledResult<LivePosition[]> => result.status === 'fulfilled')
        const successful = fulfilled.flatMap((result) => result.value)
        if (fulfilled.length === 0) {
          const failure = responses.find((result): result is PromiseRejectedResult => result.status === 'rejected')
          throw failure?.reason
        }
        if (signal?.aborted || request !== requestSequence.current) return
        const complete = responses.every((result) => result.status === 'fulfilled')
        const previous = positionsAccountKey.current === accountKey ? positionsRef.current : []
        const data = [...new Map((complete ? successful : [...previous, ...successful])
          .map((position) => [position.id, position])).values()]
        data.sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
        positionsRef.current = data
        positionsAccountKey.current = accountKey
        hasActivePositions.current = hasActiveLiveExposure(data)
        setExposure({ accountKey, active: hasActivePositions.current })
        setPositions(data)
        reschedulePolling.current()
        setLoadedAccountKey(accountKey)
        setError(complete ? null : 'Some live venue positions could not be refreshed. Retrying.')
      } catch (e) {
        if (signal?.aborted || request !== requestSequence.current) return
        streamConnected.current = false
        reschedulePolling.current()
        setError(userErrorMessage(e, 'Unable to load live positions. Please try again.'))
      } finally {
        if (!signal?.aborted && request === requestSequence.current) setLoading(false)
      }
    })
    if (refreshQueued.current && !signal?.aborted) {
      refreshQueued.current = false
      window.setTimeout(() => void fetchRef.current(signal), 0)
    }
  }, [accountKey, accountPairs])
  fetchRef.current = fetch_

  useEffect(() => {
    streamConnected.current = false
    hasActivePositions.current = null
    if (!shouldMonitor || accountPairs.length === 0) return
    const connectedStreams = new Set<string>()
    const subscriptions = accountPairs.map((accounts) => {
      const key = liveAccountsKey(accounts)
      return subscribeLiveAccountEvents(accounts, (event) => {
        if (event.type === 'connected') connectedStreams.add(key)
        else if (event.type === 'disconnected') connectedStreams.delete(key)
        else if (event.type === 'positions') {
          refreshQueued.current = true
          void fetch_()
        }
        streamConnected.current = connectedStreams.size === accountPairs.length
      })
    })
    return () => subscriptions.forEach((unsubscribe) => unsubscribe())
  }, [accountKey, accountPairs, fetch_, shouldMonitor])

  useEffect(() => {
    if (!shouldMonitor) return
    const controller = new AbortController()
    const initial = window.setTimeout(() => fetch_(controller.signal), 0)
    let timer = 0
    const schedule = () => {
      window.clearTimeout(timer)
      const delay = livePositionPollInterval(hasActivePositions.current, pollInterval)
      timer = window.setTimeout(async () => {
        if (!streamConnected.current) await fetch_(controller.signal)
        if (!controller.signal.aborted) schedule()
      }, delay)
    }
    reschedulePolling.current = schedule
    schedule()
    return () => {
      if (reschedulePolling.current === schedule) reschedulePolling.current = () => {}
      window.clearTimeout(initial)
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [fetch_, pollInterval, shouldMonitor])

  const refetch = useCallback(() => fetch_(), [fetch_])

  return {
    positions: loadedAccountKey === accountKey ? positions : [],
    loading: accountKey !== '' && loadedAccountKey !== accountKey ? true : loading,
    error,
    refetch,
  }
}
