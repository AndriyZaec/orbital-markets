import { useState, useCallback, useEffect, useRef } from 'react'
import { apiError, apiFetch, userErrorMessage } from '@/lib/api'
import { leverageCapabilityMessage } from '@/lib/leverage'
import { usePageVisibility } from './usePageVisibility'
import type { LeverageCapability } from './useOpportunities'

type LiqRiskLevel = 'safe' | 'elevated' | 'warning' | 'critical' | ''

interface Leg {
  venue: string
  asset: string
  market_key?: string
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
  best_price_capacity: number
  max_leverage: number
  leverage: LeverageConfig
  leg_1: Leg
  leg_2: Leg
  expected_spread: number
  estimated_net_edge: number
  bounds: Bounds
  risk_tier: 'conservative' | 'standard' | 'aggressive' | 'experimental'
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
  accounts?: Record<string, string>,
) {
  const [plan, setPlan] = useState<ExecutionPlan | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [maxLeverageResult, setMaxLeverageResult] = useState<{ key: string; value: number | null }>({ key: '', value: null })
  const [capabilityErrorKey, setCapabilityErrorKey] = useState('')
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const requestSequence = useRef(0)
  const pageVisible = usePageVisibility()
  const accountsKey = JSON.stringify(Object.entries(accounts ?? {}).sort(([left], [right]) => left.localeCompare(right)))
  const planKey = JSON.stringify([opportunityId, requestedNotional ?? null, accountsKey])
  const maxLeverage = maxLeverageResult.key === planKey ? maxLeverageResult.value : null
  const leverageCapabilityBlocked = capabilityErrorKey === planKey

  const fetchPlan = useCallback(async (
    oppId: string,
    selectedLeverage: number,
    notional?: number,
    serializedAccounts?: string,
    requestKey?: string,
    signal?: AbortSignal,
    background = false,
  ) => {
    const requestId = ++requestSequence.current
    try {
      if (!background) setLoading(true)
      const body: Record<string, unknown> = {
        opportunity_id: oppId,
        leverage: selectedLeverage,
      }
      if (typeof notional === 'number' && notional > 0) {
        body.requested_notional = notional
      }
      const accountEntries = JSON.parse(serializedAccounts ?? '[]') as [string, string][]
      if (accountEntries.length > 0) body.accounts = Object.fromEntries(accountEntries)
      const resp = await apiFetch('/api/v1/plan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal,
      })
      if (!resp.ok) {
        const body: { error?: string; pair_max_leverage?: number; venue?: string; capability?: LeverageCapability } = await resp.json().catch(() => ({}))
        if (signal?.aborted || requestId !== requestSequence.current) return
        if (typeof body.pair_max_leverage === 'number') {
          setMaxLeverageResult({ key: requestKey ?? '', value: body.pair_max_leverage })
        } else {
          setMaxLeverageResult({ key: requestKey ?? '', value: null })
        }
        const capabilityMessage = body.capability
          ? leverageCapabilityMessage({ [body.venue ?? 'venue']: body.capability })
          : null
        setCapabilityErrorKey(capabilityMessage ? requestKey ?? '' : '')
        throw apiError(resp.status, 'Unable to build an execution plan. Please try again.', capabilityMessage
          ? { ...body, error: capabilityMessage }
          : body)
      }
      const data: ExecutionPlan = await resp.json()
      if (signal?.aborted || requestId !== requestSequence.current) return
      setPlan(data)
      setMaxLeverageResult({ key: requestKey ?? '', value: data.max_leverage })
      setCapabilityErrorKey('')
      setError(null)
      return data
    } catch (e) {
      if (signal?.aborted || requestId !== requestSequence.current) return
      setError(userErrorMessage(e, 'Unable to build an execution plan. Please try again.'))
      setPlan(null)
    } finally {
      if (!signal?.aborted && requestId === requestSequence.current) {
        setLoading(false)
      }
    }
  }, [])

  useEffect(() => {
    if (!opportunityId) {
      requestSequence.current++
      const resetId = window.setTimeout(() => {
        setPlan(null)
        setError(null)
        setMaxLeverageResult({ key: '', value: null })
        setCapabilityErrorKey('')
      }, 0)
      return () => window.clearTimeout(resetId)
    }
    if (!pageVisible) return

    const controller = new AbortController()
    const initialId = window.setTimeout(
      () => fetchPlan(opportunityId, leverage, requestedNotional, accountsKey, planKey, controller.signal),
      0,
    )

    intervalRef.current = setInterval(
      () => fetchPlan(opportunityId, leverage, requestedNotional, accountsKey, planKey, controller.signal, true),
      10_000,
    )
    return () => {
      controller.abort()
      window.clearTimeout(initialId)
      if (intervalRef.current) window.clearInterval(intervalRef.current)
    }
  }, [opportunityId, leverage, requestedNotional, accountsKey, planKey, fetchPlan, pageVisible])

  const clear = useCallback(() => {
    requestSequence.current++
    setPlan(null)
    setError(null)
    setMaxLeverageResult({ key: '', value: null })
    setCapabilityErrorKey('')
  }, [])

  const refresh = useCallback(() => {
    if (!opportunityId) return Promise.resolve(undefined)
    return fetchPlan(opportunityId, leverage, requestedNotional, accountsKey, planKey)
  }, [accountsKey, fetchPlan, leverage, opportunityId, planKey, requestedNotional])

  return { plan, loading, error, maxLeverage, leverageCapabilityBlocked, clear, refresh }
}

export type { ExecutionPlan, Leg, Bounds }
