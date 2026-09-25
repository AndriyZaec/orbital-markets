import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import { usePageVisibility } from './usePageVisibility'

type OpportunitySignalStatus = 'persistent' | 'intermittent' | 'new' | 'choppy' | 'reversed' | 'faded' | 'flat' | 'limited'

interface OpportunitySignal {
  status: OpportunitySignalStatus
  activity: number
  direction_consistency: number
  average_edge: number
  samples: number
}

type OpportunityStatus = 'available' | 'degraded' | 'unavailable'
type LeverageCapabilityStatus = 'known' | 'pending' | 'stale' | 'missing' | 'unsupported' | 'out_of_range'

interface LeverageCapability {
  status: LeverageCapabilityStatus
  maximum?: number
  requested_notional: number
  account_revision: number
  bracket_revision: number
  observed_at?: string
  expires_at?: string
  reason?: string
}

interface OpportunityAvailabilityReason {
  code: 'source_fetch_failed' | 'market_data_unavailable'
  venue?: string
}

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
  leverage_capabilities?: Record<string, LeverageCapability>
  liquidity: 'deep' | 'medium' | 'thin' | 'toxic'
  liq_suspect: boolean
  confidence: 'low' | 'medium' | 'high'
  risk_tier: 'conservative' | 'standard' | 'aggressive' | 'experimental'
  status: OpportunityStatus
  generation: number
  source_revisions: Record<string, number>
  availability_reasons: OpportunityAvailabilityReason[] | null
  execution_status: 'executable' | 'blocked'
  risk_flags: string[] | null
  warnings: string[] | null
  signal_7d: OpportunitySignal | null
}

// Default poll matches the backend scanner's 60s refresh cadence. Polling
// faster just moves the same data around; a manual refetch is still available
// on the returned object for user-triggered refreshes.
export function useOpportunities(accounts?: Record<string, string>, pollInterval = 60_000) {
  const [opportunities, setOpportunities] = useState<Opportunity[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const pageVisible = usePageVisibility()
  const requestSequence = useRef(0)
  const accountsKey = JSON.stringify(Object.entries(accounts ?? {}).sort(([left], [right]) => left.localeCompare(right)))
  const hasAsterAccount = Object.keys(accounts ?? {}).some((venue) => venue.toLowerCase() === 'aster')
  const capabilityRefreshPending = hasAsterAccount && opportunities.some((opportunity) =>
    Object.values(opportunity.leverage_capabilities ?? {}).some((capability) =>
      capability.status === 'pending' || capability.status === 'stale'))
  const effectivePollInterval = capabilityRefreshPending ? Math.min(pollInterval, 5_000) : pollInterval

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    const request = ++requestSequence.current
    try {
      const params = new URLSearchParams()
      const accountEntries = JSON.parse(accountsKey) as [string, string][]
      for (const [venue, account] of accountEntries) params.set(`accounts[${venue}]`, account)
      const query = params.toString()
      const resp = await apiFetch(`/api/v1/opportunities${query ? `?${query}` : ''}`, { signal })
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
  }, [accountsKey])

  useEffect(() => {
    if (!pageVisible) return
    const controller = new AbortController()
    const initialId = window.setTimeout(() => fetch_(controller.signal), 0)
    const intervalId = window.setInterval(() => fetch_(controller.signal), effectivePollInterval)
    return () => {
      controller.abort()
      window.clearTimeout(initialId)
      window.clearInterval(intervalId)
    }
  }, [effectivePollInterval, fetch_, pageVisible])

  return { opportunities, loading, error, lastUpdated, refetch: fetch_ }
}

export type { LeverageCapability, LeverageCapabilityStatus, Opportunity, OpportunityAvailabilityReason, OpportunitySignal, OpportunitySignalStatus, OpportunityStatus }
