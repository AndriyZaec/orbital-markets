import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch } from '@/lib/api'
import { usePageVisibility } from './usePageVisibility'

interface Fill {
  venue: string
  side: 'long' | 'short'
  target_size: number
  filled_size: number
  fill_price: number
  slippage: number
  fee: number
  filled_at: string
  current_price: number
  current_funding: number
  accum_funding: number
  next_funding_at: string | null
  leg_price_pnl: number
  liquidation_price: number
  liquidation_dist: number
  liq_risk: '' | 'safe' | 'elevated' | 'warning' | 'critical'
}

interface Event {
  from_state: string
  to_state: string
  reason: string
  at: string
}

interface PaperPosition {
  id: string
  plan_id: string
  opportunity_id: string
  asset: string
  direction: string
  venue_pair: { venue_a: string; venue_b: string }
  state: string
  leg_1_fill: Fill | null
  leg_2_fill: Fill | null
  target_notional: number
  leverage: {
    leverage: number
    margin_required: number
    gross_exposure: number
    effective_leverage: number
  }
  entry_spread: number
  current_spread: number
  hedge_mismatch: number
  close_reason: string
  price_pnl: number
  funding_pnl: number
  total_pnl: number
  realized_pnl: number
  entry_basis: number
  current_basis: number
  basis_change: number
  events: Event[]
  created_at: string
  opened_at: string | null
  closed_at: string | null
  updated_at: string
}

export function usePaperPositions(pollInterval = 5_000) {
  const [positions, setPositions] = useState<PaperPosition[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const pageVisible = usePageVisibility()
  const requestSequence = useRef(0)

  const fetchPositions = useCallback(async (signal?: AbortSignal) => {
    const request = ++requestSequence.current
    try {
      const resp = await apiFetch('/api/v1/paper/positions', { signal })
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const data: PaperPosition[] = await resp.json()
      if (signal?.aborted || request !== requestSequence.current) return
      data.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
      setPositions(data)
      setError(null)
    } catch (e) {
      if (signal?.aborted || request !== requestSequence.current) return
      setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (!signal?.aborted && request === requestSequence.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!pageVisible) return
    const controller = new AbortController()
    const initialId = window.setTimeout(() => fetchPositions(controller.signal), 0)
    const intervalId = window.setInterval(() => fetchPositions(controller.signal), pollInterval)
    return () => {
      controller.abort()
      window.clearTimeout(initialId)
      window.clearInterval(intervalId)
    }
  }, [fetchPositions, pollInterval, pageVisible])

  const closePosition = useCallback(async (posId: string) => {
    const resp = await apiFetch(`/api/v1/paper/close/${posId}`, { method: 'POST' })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${resp.status}`)
    }
    await fetchPositions()
  }, [fetchPositions])

  return { positions, loading, error, closePosition, refetch: fetchPositions }
}

export type { PaperPosition, Fill, Event }
