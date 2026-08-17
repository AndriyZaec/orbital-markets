import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import { usePageVisibility } from './usePageVisibility'

interface Opportunity {
  id: string
  detected_at: string
  asset: string
  venue_pair: { venue_a: string; venue_b: string }
  direction: 'long_a_short_b' | 'long_b_short_a'
  funding_rate_a: number
  funding_rate_b: number
  funding_spread: number
  annualized_gross_edge: number
  entry_spread_estimate: number
  slippage_estimate: number
  fee_estimate: number
  estimated_net_edge: number
  available_notional: number
  best_price_capacity: number
  recommended_notional: number
  max_leverage: number
  liquidity: 'deep' | 'medium' | 'thin' | 'toxic'
  liq_suspect: boolean
  confidence: 'low' | 'medium' | 'high'
  risk_tier: 'conservative' | 'standard' | 'aggressive' | 'experimental'
  execution_status: 'executable' | 'blocked'
  risk_flags: string[] | null
  warnings: string[] | null
}

// Default poll matches the backend scanner's 60s refresh cadence. Polling
// faster just moves the same data around; a manual refetch is still available
// on the returned object for user-triggered refreshes.
export function useOpportunities(pollInterval = 60_000) {
  const [opportunities, setOpportunities] = useState<Opportunity[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const pageVisible = usePageVisibility()
  const requestSequence = useRef(0)

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    const request = ++requestSequence.current
    try {
      const resp = await apiFetch('/api/v1/opportunities', { signal })
      if (!resp.ok) throw await apiResponseError(resp, 'Unable to load opportunities. Please try again.')
      const data: Opportunity[] = await resp.json()
      if (signal?.aborted || request !== requestSequence.current) return
      setOpportunities(data)
      setLastUpdated(new Date())
      setError(null)
    } catch (e) {
      if (signal?.aborted || request !== requestSequence.current) return
      setError(userErrorMessage(e, 'Unable to load opportunities. Please try again.'))
    } finally {
      if (!signal?.aborted && request === requestSequence.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!pageVisible) return
    const controller = new AbortController()
    const initialId = window.setTimeout(() => fetch_(controller.signal), 0)
    const intervalId = window.setInterval(() => fetch_(controller.signal), pollInterval)
    return () => {
      controller.abort()
      window.clearTimeout(initialId)
      window.clearInterval(intervalId)
    }
  }, [fetch_, pollInterval, pageVisible])

  return { opportunities, loading, error, lastUpdated, refetch: fetch_ }
}

export type { Opportunity }
