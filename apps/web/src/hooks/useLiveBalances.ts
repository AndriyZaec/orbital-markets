import { useEffect, useState, useCallback } from 'react'
import { apiFetch } from '@/lib/api'

interface VenueBalance {
  venue: string
  equity: number
  available: number
  connected: boolean
}

interface Balances {
  pacifica: VenueBalance
  hyperliquid: VenueBalance
}

const EMPTY: Balances = {
  pacifica: { venue: 'pacifica', equity: 0, available: 0, connected: false },
  hyperliquid: { venue: 'hyperliquid', equity: 0, available: 0, connected: false },
}

export function useLiveBalances(pollInterval = 5_000) {
  const [balances, setBalances] = useState<Balances>(EMPTY)

  const fetch_ = useCallback(async () => {
    try {
      const resp = await apiFetch('/api/v1/live/balances')
      if (!resp.ok) return
      const data: Balances = await resp.json()
      setBalances(data)
    } catch {
      // silently ignore — balance display is best-effort
    }
  }, [])

  useEffect(() => {
    fetch_()
    const id = setInterval(fetch_, pollInterval)
    return () => clearInterval(id)
  }, [fetch_, pollInterval])

  return balances
}
