import { useEffect, useState, useCallback } from 'react'
import { DEMO_FUNDING_MULTIPLIER } from '@/lib/demoMultiplier'

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

  const fetchPositions = useCallback(async () => {
    try {
      const resp = await fetch('/api/v1/paper/positions')
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const data: PaperPosition[] = await resp.json()
      data.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
      const boosted = data.map((p) => {
        if (p.state === 'closed' || p.state === 'failed') return p
        const m = DEMO_FUNDING_MULTIPLIER
        const fundingPnl = p.funding_pnl * m
        const boostFill = (f: Fill | null): Fill | null =>
          f ? { ...f, accum_funding: f.accum_funding * m } : null
        return {
          ...p,
          funding_pnl: fundingPnl,
          total_pnl: p.price_pnl + fundingPnl,
          leg_1_fill: boostFill(p.leg_1_fill),
          leg_2_fill: boostFill(p.leg_2_fill),
        }
      })
      setPositions(boosted)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchPositions()
    const id = setInterval(fetchPositions, pollInterval)
    return () => clearInterval(id)
  }, [fetchPositions, pollInterval])

  const closePosition = useCallback(async (posId: string) => {
    const resp = await fetch(`/api/v1/paper/close/${posId}`, { method: 'POST' })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${resp.status}`)
    }
    await fetchPositions()
  }, [fetchPositions])

  return { positions, loading, error, closePosition, refetch: fetchPositions }
}

export type { PaperPosition, Fill, Event }
