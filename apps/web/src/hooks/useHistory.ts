import { useEffect, useState, useCallback, useRef } from 'react'
import { apiFetch } from '@/lib/api'

export interface HistoryPoint {
  t: string
  basis: number
  edge: number
  funding_a: number
  funding_b: number
}

interface HistoryData {
  asset: string
  venue_a: string
  venue_b: string
  points: HistoryPoint[]
}

export function useHistory(asset: string, venueA: string, venueB: string, range_: string) {
  const [data, setData] = useState<HistoryPoint[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const requestSequence = useRef(0)

  const fetchHistory = useCallback(async () => {
    const requestId = ++requestSequence.current
    try {
      setLoading(true)
      const params = new URLSearchParams({ asset, venue_a: venueA, venue_b: venueB, range: range_ })
      const resp = await apiFetch(`/api/v1/history?${params}`)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const json: HistoryData = await resp.json()
      if (requestId !== requestSequence.current) return
      setData(json.points ?? [])
      setError(null)
    } catch (e) {
      if (requestId !== requestSequence.current) return
      setError(e instanceof Error ? e.message : 'Unknown error')
      setData([])
    } finally {
      if (requestId === requestSequence.current) {
        setLoading(false)
      }
    }
  }, [asset, venueA, venueB, range_])

  useEffect(() => {
    fetchHistory()
  }, [fetchHistory])

  return { data, loading, error }
}
