import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch } from '@/lib/api'
import { usePageVisibility } from './usePageVisibility'

interface PnLBlock {
  price_pnl: number
  funding_pnl: number
  total_pnl: number
  realized_pnl: number
  unrealized_pnl: number
}

interface BreakEvenBlock {
  avg_estimated_break_even_hours: number
  reached_count: number
  not_reached_count: number
  reached_rate: number
}

interface Summary {
  total_trades: number
  closed_trades: number
  open_trades: number
  failed_trades: number
  profitable_trades: number
  unprofitable_trades: number
  pnl: PnLBlock
  avg_hold_hours: number
  break_even: BreakEvenBlock
}

interface AssetRow {
  asset: string
  total_trades: number
  closed_trades: number
  total_price_pnl: number
  total_funding_pnl: number
  total_pnl: number
  total_realized_pnl: number
  total_unrealized_pnl: number
  avg_hold_hours: number
  avg_est_break_even_hours: number
  break_even_reached_count: number
  break_even_not_reached_count: number
  profitable_trades: number
}

interface RiskTierRow {
  risk_tier: string
  total_trades: number
  closed_trades: number
  total_price_pnl: number
  total_funding_pnl: number
  total_pnl: number
  total_realized_pnl: number
  avg_hold_hours: number
  break_even_reached_count: number
  profitable_trades: number
}

interface CloseReasonRow {
  close_reason: string
  total_trades: number
  total_price_pnl: number
  total_funding_pnl: number
  total_realized_pnl: number
  avg_hold_hours: number
  profitable_trades: number
}

interface Analytics {
  mode: string
  summary: Summary
  by_asset: AssetRow[]
  by_risk_tier: RiskTierRow[]
  by_close_reason: CloseReasonRow[]
}

export function useAnalytics(pollInterval = 15_000) {
  const [data, setData] = useState<Analytics | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const pageVisible = usePageVisibility()
  const requestSequence = useRef(0)

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    const request = ++requestSequence.current
    try {
      const resp = await apiFetch('/api/v1/paper/analytics', { signal })
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const json: Analytics = await resp.json()
      if (signal?.aborted || request !== requestSequence.current) return
      setData(json)
      setError(null)
    } catch (e) {
      if (signal?.aborted || request !== requestSequence.current) return
      setError(e instanceof Error ? e.message : 'Unknown error')
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

  return { data, loading, error }
}

export type { Analytics, Summary, PnLBlock, BreakEvenBlock, AssetRow, RiskTierRow, CloseReasonRow }
