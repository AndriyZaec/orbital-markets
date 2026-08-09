import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch } from '@/lib/api'
import { runSingleFlight } from '@/lib/polling'
import { useVenueAuthority } from './useVenueAuthority'

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
  const { pacificaAddress, hyperliquidAddress } = useVenueAuthority()
  const accountKey = pacificaAddress && hyperliquidAddress
    ? `${pacificaAddress}:${hyperliquidAddress.toLowerCase()}`
    : ''

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
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
        const data: LivePosition[] = await resp.json()
        if (signal?.aborted || request !== requestSequence.current) return
        data.sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
        setPositions(data)
        setLoadedAccountKey(accountKey)
        setError(null)
      } catch (e) {
        if (signal?.aborted || request !== requestSequence.current) return
        setPositions([])
        setLoadedAccountKey(accountKey)
        setError(e instanceof Error ? e.message : 'Unknown error')
      } finally {
        if (!signal?.aborted && request === requestSequence.current) setLoading(false)
      }
    })
  }, [pacificaAddress, hyperliquidAddress, accountKey])

  useEffect(() => {
    const controller = new AbortController()
    const initial = window.setTimeout(() => fetch_(controller.signal), 0)
    const id = setInterval(() => fetch_(controller.signal), pollInterval)
    return () => {
      window.clearTimeout(initial)
      clearInterval(id)
      controller.abort()
    }
  }, [fetch_, pollInterval])

  const refetch = useCallback(() => fetch_(), [fetch_])

  return {
    positions: loadedAccountKey === accountKey ? positions : [],
    loading: accountKey !== '' && loadedAccountKey !== accountKey ? true : loading,
    error,
    refetch,
  }
}
