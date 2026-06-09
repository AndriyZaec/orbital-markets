import { useEffect, useState, useCallback } from 'react'

export interface LiveFillDetail {
  id: number
  position_id: string
  leg: number
  venue: string
  symbol: string
  side: string
  order_id: string
  client_order_id: string
  requested_amount: number
  filled_amount: number
  avg_fill_price: number
  fill_ratio: number
  fee: number
  accepted: boolean
  filled: boolean
  error?: string
  filled_at: string
}

export interface LiveEventDetail {
  id: number
  position_id: string
  event: string
  state: string
  detail?: string
  at: string
}

export interface LivePositionDetailData {
  position: Record<string, unknown>
  fills: LiveFillDetail[]
  events: LiveEventDetail[]
}

export function useLivePositionDetail(positionId: string | null) {
  const [data, setData] = useState<LivePositionDetailData | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const fetch_ = useCallback(async () => {
    if (!positionId) return
    setLoading(true)
    try {
      const resp = await fetch(`/api/v1/live/positions/${positionId}`)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const d: LivePositionDetailData = await resp.json()
      setData(d)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }, [positionId])

  useEffect(() => {
    fetch_()
  }, [fetch_])

  return { data, loading, error, refetch: fetch_ }
}
