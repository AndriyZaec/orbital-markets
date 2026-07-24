import { useEffect, useState, useCallback } from 'react'
import { apiFetch } from '@/lib/api'

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

  const fetch_ = useCallback(async () => {
    try {
const resp = await apiFetch('/api/v1/opportunities')
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const data: Opportunity[] = await resp.json()
      setOpportunities(data)
      setLastUpdated(new Date())
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

  return { opportunities, loading, error, lastUpdated, refetch: fetch_ }
}

export type { Opportunity }
