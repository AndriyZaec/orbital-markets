import { useQuery } from '@tanstack/react-query'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import { liveAccountsKey, liveAccountsQuery } from '@/lib/live-bindings'
import type { PositionPricePnLPoint } from '@/lib/position-price-pnl'

export interface PositionPriceHistory {
  position_id: string
  valuation_key: string
  points: PositionPricePnLPoint[]
  start?: string
  end?: string
}

export function usePositionPriceHistory(positionId: string, accounts: Record<string, string> | undefined, enabled: boolean, valuationKey: string) {
  const active = enabled && !!accounts && Object.values(accounts).length === 2 && Object.values(accounts).every(Boolean)
  const query = useQuery({
    queryKey: ['position-price-history', positionId, accounts ? liveAccountsKey(accounts) : '', valuationKey],
    enabled: active,
    staleTime: 60_000,
    retry: false,
    refetchInterval: active ? 60_000 : false,
    queryFn: async ({ signal }) => {
      const response = await apiFetch(`/api/v1/live/positions/${encodeURIComponent(positionId)}/price-pnl?${liveAccountsQuery(accounts ?? {})}`, { signal })
      if (!response.ok) throw await apiResponseError(response, 'Price PnL history is unavailable.')
      const history: PositionPriceHistory = await response.json()
      if (history.position_id !== positionId || history.valuation_key !== valuationKey) throw new Error('Position inputs changed. Waiting for an updated Price PnL snapshot.')
      return history
    },
  })
  return { data: query.data, loading: active && query.isLoading, error: query.error ? userErrorMessage(query.error, 'Price PnL history is unavailable.') : null }
}
