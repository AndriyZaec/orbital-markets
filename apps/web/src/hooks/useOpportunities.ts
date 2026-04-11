import { useEffect, useState, useCallback } from 'react'

interface Opportunity {
  id: string
  detected_at: string
  asset: string
  venue_pair: { venue_a: string; venue_b: string }
  direction: string
  funding_rate_a: number
  funding_rate_b: number
  funding_spread: number
  annualized_gross_edge: number
  entry_spread_estimate: number
  confidence: 'low' | 'medium' | 'high'
  risk_tier: 'conservative' | 'standard' | 'aggressive' | 'experimental'
  executable: boolean
  warnings: string[] | null
}

export function useOpportunities(pollInterval = 10_000) {
  const [opportunities, setOpportunities] = useState<Opportunity[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetch_ = useCallback(async () => {
    try {
      const resp = await fetch('/api/v1/opportunities')
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const data: Opportunity[] = await resp.json()
      setOpportunities(data)
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

  return { opportunities, loading, error, refetch: fetch_ }
}

export type { Opportunity }
