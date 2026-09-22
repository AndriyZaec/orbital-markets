import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import { useVenueAuthority } from './useVenueAuthority'
import { liveAccountsKey, liveAccountsQuery } from '@/lib/live-bindings'
import type { Venue } from '@/agents/types'
import type { LivePositionChartContext } from '@/lib/position-chart-context'
import type { LivePosition } from './useLivePositions'

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
  position: LivePosition
  fills: LiveFillDetail[]
  events: LiveEventDetail[]
  chart_context: LivePositionChartContext
}

export function useLivePositionDetail(positionId: string | null, venues: [Venue, Venue]) {
  const { pacificaAddress, hyperliquidAddress, asterAddress } = useVenueAuthority()
  const [venueA, venueB] = venues
  const accounts = useMemo(
    () => ({
      [venueA]: authorityAddress(venueA, pacificaAddress, hyperliquidAddress, asterAddress),
      [venueB]: authorityAddress(venueB, pacificaAddress, hyperliquidAddress, asterAddress),
    }),
    [asterAddress, hyperliquidAddress, pacificaAddress, venueA, venueB],
  )

  const enabled = Boolean(positionId) && Object.values(accounts).every(Boolean)
  const query = useQuery({
    queryKey: ['live-position-detail', positionId, liveAccountsKey(accounts)],
    enabled,
    staleTime: 5_000,
    refetchInterval: enabled ? 5_000 : false,
    queryFn: async ({ signal }) => {
      const query = liveAccountsQuery(accounts)
      const resp = await apiFetch(`/api/v1/live/positions/${positionId}?${query}`, { signal })
      if (!resp.ok) throw await apiResponseError(resp, 'This position is no longer available.')
      return resp.json() as Promise<LivePositionDetailData>
    },
  })

  return {
    data: query.data ?? null,
    loading: enabled && query.isLoading,
    error: query.error ? userErrorMessage(query.error, 'Unable to load position details. Please try again.') : null,
    refetch: query.refetch,
  }
}

function authorityAddress(
  venue: Venue,
  pacifica: string | null,
  hyperliquid: string | null,
  aster: string | null,
): string {
  return (venue === 'pacifica' ? pacifica : venue === 'aster' ? aster : hyperliquid) ?? ''
}
