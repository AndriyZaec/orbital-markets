import { useState, useCallback, useEffect, useRef } from 'react'
import { apiFetch } from '@/lib/api'

interface Leg {
  venue: string
  asset: string
  side: 'long' | 'short'
  expected_price: number
  slippage: number
  fee: number
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

export function usePlan(opportunityId: string | null, leverage: number = 1) {
  const [plan, setPlan] = useState<ExecutionPlan | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const fetchPlan = useCallback(async (oppId: string, lev: number) => {
    try {
      setLoading(true)
      const resp = await apiFetch('/api/v1/plan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ opportunity_id: oppId, leverage: lev }),
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

    fetchPlan(opportunityId, leverage)

    intervalRef.current = setInterval(() => fetchPlan(opportunityId, leverage), 10_000)
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [opportunityId, leverage, fetchPlan])

  const clear = useCallback(() => {
    setPlan(null)
    setError(null)
  }, [])

  return { plan, loading, error, clear }
}

export type { ExecutionPlan, Leg, Bounds }
