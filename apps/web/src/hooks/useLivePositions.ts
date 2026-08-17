import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import {
  livePositionPollInterval,
  runSingleFlight,
  shouldMonitorLiveUpdates,
} from '@/lib/polling'
import { hasActiveLiveExposure, subscribeLiveAccountEvents } from '@/lib/live-events'
import { useVenueAuthority } from './useVenueAuthority'
import { usePageVisibility } from './usePageVisibility'

export interface LivePosition {
  id: string
  plan_id: string
  opportunity_id: string
  asset: string
  venue_a: string
  venue_b: string
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
  const requestSequence = useRef(0)
  const polling = useRef({ running: false })
  const streamConnected = useRef(false)
  const hasActivePositions = useRef<boolean | null>(null)
  const reschedulePolling = useRef<() => void>(() => {})
  const pageVisible = usePageVisibility()
  const { pacificaAddress, hyperliquidAddress } = useVenueAuthority()
  const accountKey = pacificaAddress && hyperliquidAddress
    ? `${pacificaAddress}:${hyperliquidAddress.toLowerCase()}`
    : ''
  const [exposure, setExposure] = useState<{ accountKey: string; active: boolean | null }>({
    accountKey: '',
    active: null,
  })
  const activeExposure = exposure.accountKey === accountKey ? exposure.active : null
  const shouldMonitor = shouldMonitorLiveUpdates(pageVisible, activeExposure)

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    if (signal?.aborted) return
    if (!pacificaAddress || !hyperliquidAddress) {
      setPositions([])
      setLoadedAccountKey('')
      setLoading(false)
      setError(null)
      return
    }
    await runSingleFlight(polling.current, async () => {
      const request = ++requestSequence.current
      try {
        const query = new URLSearchParams({
          account_pacifica: pacificaAddress,
          account_hyperliquid: hyperliquidAddress,
        })
        const resp = await apiFetch(`/api/v1/live/positions?${query}`, { signal })
        if (!resp.ok) throw await apiResponseError(resp, 'Unable to load live positions. Please try again.')
        const data: LivePosition[] = await resp.json()
        if (signal?.aborted || request !== requestSequence.current) return
        data.sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
        hasActivePositions.current = hasActiveLiveExposure(data)
        setExposure({ accountKey, active: hasActivePositions.current })
        reschedulePolling.current()
        setPositions(data)
        setLoadedAccountKey(accountKey)
        setError(null)
      } catch (e) {
        if (signal?.aborted || request !== requestSequence.current) return
        setPositions([])
        setLoadedAccountKey(accountKey)
        setError(userErrorMessage(e, 'Unable to load live positions. Please try again.'))
      } finally {
        if (!signal?.aborted && request === requestSequence.current) setLoading(false)
      }
    })
  }, [pacificaAddress, hyperliquidAddress, accountKey])

  useEffect(() => {
    streamConnected.current = false
    hasActivePositions.current = null
    if (!shouldMonitor || !pacificaAddress || !hyperliquidAddress) return
    return subscribeLiveAccountEvents(pacificaAddress, hyperliquidAddress, (event) => {
      if (event.type === 'connected') streamConnected.current = true
      else if (event.type === 'disconnected') streamConnected.current = false
      else if (event.type === 'positions') {
        requestSequence.current++
        const data = event.data as LivePosition[]
        data.sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
        hasActivePositions.current = hasActiveLiveExposure(data)
        setExposure({ accountKey, active: hasActivePositions.current })
        reschedulePolling.current()
        setPositions(data)
        setLoadedAccountKey(accountKey)
        setLoading(false)
        setError(null)
      }
    })
  }, [shouldMonitor, pacificaAddress, hyperliquidAddress, accountKey])

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
