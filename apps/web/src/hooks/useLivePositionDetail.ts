import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch } from '@/lib/api'
import { useVenueAuthority } from './useVenueAuthority'

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
  const requestSequence = useRef(0)
  const { pacificaAddress, hyperliquidAddress } = useVenueAuthority()

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    const request = ++requestSequence.current
    if (signal?.aborted) return
    setData(null)
    setError(null)
    if (!positionId || !pacificaAddress || !hyperliquidAddress) {
      setLoading(false)
      return
    }
    setLoading(true)
    try {
      const query = new URLSearchParams({
        account_pacifica: pacificaAddress,
        account_hyperliquid: hyperliquidAddress,
      })
      const resp = await apiFetch(`/api/v1/live/positions/${positionId}?${query}`, { signal })
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const d: LivePositionDetailData = await resp.json()
      if (signal?.aborted || request !== requestSequence.current) return
      setData(d)
    } catch (e) {
      if (signal?.aborted || request !== requestSequence.current) return
      setData(null)
      setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (request === requestSequence.current) setLoading(false)
    }
  }, [positionId, pacificaAddress, hyperliquidAddress])

  useEffect(() => {
    const controller = new AbortController()
    const timer = window.setTimeout(() => fetch_(controller.signal), 0)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [fetch_])

  const refetch = useCallback(() => fetch_(), [fetch_])

  return { data, loading, error, refetch }
}
