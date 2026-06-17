import { useEffect, useState, useCallback } from 'react'
import { apiFetch } from '@/lib/api'

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
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetch_ = useCallback(async () => {
    try {
      const resp = await apiFetch('/api/v1/live/positions')
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const data: LivePosition[] = await resp.json()
      data.sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
      setPositions(data)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetch_()
    const id = setInterval(fetch_, pollInterval)
    return () => clearInterval(id)
  }, [fetch_, pollInterval])

  return { positions, loading, error, refetch: fetch_ }
}
