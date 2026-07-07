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
  leverageLong: number = 1,
  leverageShort: number = 1,
  requestedNotional?: number,
) {
  const [plan, setPlan] = useState<ExecutionPlan | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const fetchPlan = useCallback(async (oppId: string, levLong: number, levShort: number, notional?: number) => {
    try {
      setLoading(true)
      // `leverage` is kept as a shared fallback for older backends; per-leg
      // fields are the source of truth today.
      const body: Record<string, unknown> = {
        opportunity_id: oppId,
        leverage: levLong,
        leverage_long: levLong,
        leverage_short: levShort,
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
        const body = await resp.json().catch(() => ({}))
        throw new Error(body.error || `HTTP ${resp.status}`)
      }
      const data: ExecutionPlan = await resp.json()
      setPlan(data)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unknown error')
      setPlan(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!opportunityId) {
      setPlan(null)
      setError(null)
      return
    }

    fetchPlan(opportunityId, leverageLong, leverageShort, requestedNotional)

    intervalRef.current = setInterval(
      () => fetchPlan(opportunityId, leverageLong, leverageShort, requestedNotional),
      10_000,
    )
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [opportunityId, leverageLong, leverageShort, requestedNotional, fetchPlan])

  const clear = useCallback(() => {
    setPlan(null)
    setError(null)
  }, [])

  return { plan, loading, error, clear }
}

export type { ExecutionPlan, Leg, Bounds }
