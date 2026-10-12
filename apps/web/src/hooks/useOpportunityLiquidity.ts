import { useEffect, useState } from 'react'
import { apiFetch } from '@/lib/api'
import { usePageVisibility } from './usePageVisibility'

interface LiquidityEstimate {
  available: boolean
  capacity?: number
  impact_budget?: number
  book_limited?: boolean
  observed_at?: string
  direction?: string
}

// Optional read-only context for one selected pair; never changes order sizing.
export function useOpportunityLiquidity(id: string, direction: string, enabled: boolean) {
  const visible = usePageVisibility()
  const key = `${id}:${direction}`
  const [state, setState] = useState<{ key: string; estimate: LiquidityEstimate | null }>({ key: '', estimate: null })

  useEffect(() => {
    if (!enabled || !visible) return
    const controller = new AbortController()
    let inFlight = false
    const load = async () => {
      if (inFlight) return
      inFlight = true
      try {
        const response = await apiFetch(`/api/v1/opportunities/${encodeURIComponent(id)}/liquidity`, { signal: controller.signal })
        const estimate: LiquidityEstimate | null = response.ok ? await response.json() : null
        if (!controller.signal.aborted) setState({ key, estimate: estimate?.direction === direction ? estimate : null })
      } catch {
        if (!controller.signal.aborted) setState({ key, estimate: null })
      } finally { inFlight = false }
    }
    void load()
    const timer = window.setInterval(() => { void load() }, 30_000)
    return () => { controller.abort(); window.clearInterval(timer) }
  }, [id, direction, enabled, visible, key])

  const estimate = enabled && state.key === key ? state.estimate : null
  return estimate?.available && typeof estimate.capacity === 'number' && Number.isFinite(estimate.capacity) && estimate.capacity > 0
    ? { ...estimate, capacity: estimate.capacity }
    : null
}
