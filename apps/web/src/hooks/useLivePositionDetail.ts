import { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import { useVenueAuthority } from './useVenueAuthority'
import { liveAccountsQuery } from '@/lib/live-bindings'
import type { Venue } from '@/agents/types'

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

export function useLivePositionDetail(positionId: string | null, venues: [Venue, Venue]) {
  const [data, setData] = useState<LivePositionDetailData | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const requestSequence = useRef(0)
  const { pacificaAddress, hyperliquidAddress, asterAddress } = useVenueAuthority()
  const [venueA, venueB] = venues
  const accounts = useMemo(
    () => ({
      [venueA]: authorityAddress(venueA, pacificaAddress, hyperliquidAddress, asterAddress),
      [venueB]: authorityAddress(venueB, pacificaAddress, hyperliquidAddress, asterAddress),
    }),
    [asterAddress, hyperliquidAddress, pacificaAddress, venueA, venueB],
  )

  const fetch_ = useCallback(async (signal?: AbortSignal) => {
    const request = ++requestSequence.current
    if (signal?.aborted) return
    setData(null)
    setError(null)
    if (!positionId || Object.values(accounts).some((account) => !account)) {
      setLoading(false)
      return
    }
    setLoading(true)
    try {
      const query = liveAccountsQuery(accounts)
      const resp = await apiFetch(`/api/v1/live/positions/${positionId}?${query}`, { signal })
      if (!resp.ok) throw await apiResponseError(resp, 'This position is no longer available.')
      const d: LivePositionDetailData = await resp.json()
      if (signal?.aborted || request !== requestSequence.current) return
      setData(d)
    } catch (e) {
      if (signal?.aborted || request !== requestSequence.current) return
      setData(null)
      setError(userErrorMessage(e, 'Unable to load position details. Please try again.'))
    } finally {
      if (request === requestSequence.current) setLoading(false)
    }
  }, [positionId, accounts])

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

function authorityAddress(
  venue: Venue,
  pacifica: string | null,
  hyperliquid: string | null,
  aster: string | null,
): string {
  return (venue === 'pacifica' ? pacifica : venue === 'aster' ? aster : hyperliquid) ?? ''
}
