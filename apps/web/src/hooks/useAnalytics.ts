import { useEffect, useState, useCallback } from 'react'

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

  const fetch_ = useCallback(async () => {
    try {
      const resp = await fetch('/api/v1/paper/analytics')
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const json: Analytics = await resp.json()
      setData(json)
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

  return { data, loading, error }
}

export type { Analytics, Summary, PnLBlock, BreakEvenBlock, AssetRow, RiskTierRow, CloseReasonRow }
