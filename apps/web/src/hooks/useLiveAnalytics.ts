import { useEffect, useState } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import { usePageVisibility } from './usePageVisibility'

export interface LiveVolumeWindow {
  gross_venue_volume: number
  hedged_trade_volume: number
  open_volume: number
  close_volume: number
}

export interface LiveVolumeBreakdown {
  key: string
  gross_venue_volume: number
  open_volume: number
  close_volume: number
}

export interface LiveAnalytics {
  generated_at: string
  volume: {
    all_time: LiveVolumeWindow
    last_24h: LiveVolumeWindow
    last_7d: LiveVolumeWindow
    by_venue: LiveVolumeBreakdown[]
    by_asset: LiveVolumeBreakdown[]
  }
  trades: {
    open_positions: number
    degraded_positions: number
    successful_opens: number
    failed_opens: number
    closed_trades: number
  }
}

export function useLiveAnalytics(accessToken: string, pollInterval = 60_000) {
  const [data, setData] = useState<LiveAnalytics | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [requiresToken, setRequiresToken] = useState(false)
  const pageVisible = usePageVisibility()

  useEffect(() => {
    if (!pageVisible) return
    const controller = new AbortController()
    const headers = accessToken ? { 'X-Analytics-Token': accessToken } : undefined

    async function fetchMetrics() {
      try {
        const response = await apiFetch('/api/v1/analytics', {
          signal: controller.signal,
          headers,
        })
        if (response.status === 404) {
          setRequiresToken(true)
          setError(null)
          setLoading(false)
          return
        }
        if (!response.ok) throw await apiResponseError(response, 'Unable to load live analytics.')
        setData(await response.json() as LiveAnalytics)
        setRequiresToken(false)
        setError(null)
      } catch (requestError) {
        if (controller.signal.aborted) return
        setError(userErrorMessage(requestError, 'Unable to load live analytics.'))
      } finally {
        if (!controller.signal.aborted) setLoading(false)
      }
    }

    fetchMetrics()
    const interval = window.setInterval(fetchMetrics, pollInterval)
    return () => {
      controller.abort()
      window.clearInterval(interval)
    }
  }, [accessToken, pageVisible, pollInterval])

  return { data, loading, error, requiresToken }
}
