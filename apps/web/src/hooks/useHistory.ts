import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'

export interface HistoryPoint {
  t: string
  basis: number
  price_spread?: number | null
  mark_price_a?: number | null
  mark_price_b?: number | null
  edge: number
  funding_a: number
  funding_b: number
}

interface HistoryData {
  asset: string
  venue_a: string
  venue_b: string
  points: HistoryPoint[]
  start?: string
  end?: string
}

export interface HistoryWindow {
  start: string
  end: string
}

export function useHistory(asset: string, venueA: string, venueB: string, range_: string, series: 'funding' | 'price_spread' | null = 'funding') {
  const queryKey = JSON.stringify([asset, venueA, venueB, range_, series])
  const [history, setHistory] = useState<{ key: string; data: HistoryPoint[]; window?: HistoryWindow }>({ key: '', data: [] })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const requestSequence = useRef(0)

  const fetchHistory = useCallback(async () => {
    if (series === null) return
    const requestId = ++requestSequence.current
    try {
      setLoading(true)
      const params = new URLSearchParams({ asset, venue_a: venueA, venue_b: venueB, range: range_ })
      if (series === 'price_spread') params.set('series', series)
      const resp = await apiFetch(`/api/v1/history?${params}`)
      if (!resp.ok) throw await apiResponseError(resp, 'Funding history is not available. Please try again.')
      const json: HistoryData = await resp.json()
      if (requestId !== requestSequence.current) return
      setHistory({ key: queryKey, data: json.points ?? [], window: json.start && json.end ? { start: json.start, end: json.end } : undefined })
      setError(null)
    } catch (e) {
      if (requestId !== requestSequence.current) return
      setError(userErrorMessage(e, 'Funding history is not available. Please try again.'))
      setHistory({ key: queryKey, data: [] })
    } finally {
      if (requestId === requestSequence.current) {
        setLoading(false)
      }
    }
  }, [asset, venueA, venueB, range_, series, queryKey])

  useEffect(() => {
    if (series === null) return
    const initialId = window.setTimeout(fetchHistory, 0)
    return () => window.clearTimeout(initialId)
  }, [fetchHistory, series])

  const current = history.key === queryKey
  return { data: current ? history.data : [], window: current ? history.window : undefined, loading: series !== null && (loading || !current), error: series !== null && current ? error : null }
}
