import { useState, useCallback, useEffect, useRef } from 'react'
import { apiFetch } from '@/lib/api'

type LiqRiskLevel = 'safe' | 'elevated' | 'warning' | 'critical' | ''

interface Leg {
  venue: string
  asset: string
  side: 'long' | 'short'
  expected_price: number
  slippage: number
  fee: number
  // Per-leg leverage / margin. Notional is equal on both legs and lives on the
  // plan; leverage & margin are per-leg since the user picks them per-leg.
  leverage: number
  margin_required: number
  // Backend-computed estimated liquidation. liquidation_price = 0 => not
  // practically liquidatable (1x). liquidation_risk is '' at 1x.
  liquidation_price: number
  liquidation_distance: number
  liquidation_risk?: LiqRiskLevel
}

interface Bounds {
  max_slippage_pct: number
  max_entry_spread_pct: number
  min_net_edge_pct: number
}

interface LeverageConfig {
  leverage: number
  margin_required: number
  gross_exposure: number
  effective_leverage: number
}

interface ExecutionPlan {
  id: string
  opportunity_id: string
  asset: string
  direction: string
  notional: number
  max_leverage: number
  leverage: LeverageConfig
  leg_1: Leg
  leg_2: Leg
  expected_spread: number
  estimated_net_edge: number
  bounds: Bounds
  confidence: 'low' | 'medium' | 'high'
  executable: boolean
  warnings: string[] | null
  created_at: string
  expires_at: string
}

export function usePlan(
  opportunityId: string | null,
  leverage: number = 1,
  requestedNotional?: number,
) {
  const [plan, setPlan] = useState<ExecutionPlan | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [maxLeverage, setMaxLeverage] = useState<number | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const requestSequence = useRef(0)

  const fetchPlan = useCallback(async (oppId: string, selectedLeverage: number, notional?: number) => {
    const requestId = ++requestSequence.current
    try {
      setLoading(true)
      const body: Record<string, unknown> = {
        opportunity_id: oppId,
        leverage: selectedLeverage,
      }
      if (typeof notional === 'number' && notional > 0) {
        body.requested_notional = notional
      }
      const resp = await apiFetch('/api/v1/plan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!resp.ok) {
        const body: { error?: string; pair_max_leverage?: number } = await resp.json().catch(() => ({}))
        if (requestId !== requestSequence.current) return
        if (typeof body.pair_max_leverage === 'number') {
          setMaxLeverage(body.pair_max_leverage)
        }
        throw new Error(body.error || `HTTP ${resp.status}`)
      }
      const data: ExecutionPlan = await resp.json()
      if (requestId !== requestSequence.current) return
      setPlan(data)
      setMaxLeverage(data.max_leverage)
      setError(null)
    } catch (e) {
      if (requestId !== requestSequence.current) return
      setError(e instanceof Error ? e.message : 'Unknown error')
      setPlan(null)
    } finally {
      if (requestId === requestSequence.current) {
        setLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    if (!opportunityId) {
      requestSequence.current++
      setPlan(null)
      setError(null)
      setMaxLeverage(null)
      return
    }

    fetchPlan(opportunityId, leverage, requestedNotional)

    intervalRef.current = setInterval(
      () => fetchPlan(opportunityId, leverage, requestedNotional),
      10_000,
    )
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [opportunityId, leverage, requestedNotional, fetchPlan])

  const clear = useCallback(() => {
    requestSequence.current++
    setPlan(null)
    setError(null)
    setMaxLeverage(null)
  }, [])

  return { plan, loading, error, maxLeverage, clear }
}

export type { ExecutionPlan, Leg, Bounds }
