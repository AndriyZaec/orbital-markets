import { useEffect, useState, useCallback } from 'react'

export interface HistoryPoint {
  t: string
  basis: number
  edge: number
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

  const fetchHistory = useCallback(async () => {
    try {
      setLoading(true)
      const params = new URLSearchParams({ asset, venue_a: venueA, venue_b: venueB, range: range_ })
      const resp = await fetch(`/api/v1/history?${params}`)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      const json: HistoryData = await resp.json()
      setData(json.points ?? [])
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unknown error')
      setData([])
    } finally {
      setLoading(false)
    }
  }, [asset, venueA, venueB, range_])

  useEffect(() => {
    fetchHistory()
  }, [fetchHistory])

  return { data, loading, error }
}
