import { createContext, createElement, useContext, useEffect, useState, useCallback, useMemo, useRef, type ReactNode } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import {
  livePositionPollInterval,
  shouldMonitorLiveUpdates,
} from '@/lib/polling'
import { hasActiveLiveExposure, subscribeLiveAccountEvents } from '@/lib/live-events'
import { useVenueAuthority } from './useVenueAuthority'
import { usePageVisibility } from './usePageVisibility'
import { liveAccountsKey, liveAccountsQuery } from '@/lib/live-bindings'
import type { Venue } from '@/agents/types'
import { accountRead } from '@/lib/account-read'

export interface LivePosition {
  id: string
  plan_id: string
  opportunity_id: string
  asset: string
  venue_a: Venue
  venue_b: Venue
  state: string
  notional: number
  leverage: number
  entry_spread: number
  hedge_mismatch: number
  current_spread: number
  current_basis: number
  entry_basis: number
  basis_change: number
  price_pnl: number
  funding_pnl: number
  funding_pnl_source: 'pending' | 'estimated' | 'realized'
  total_pnl: number
  phoenix_pending_funding?: {
    amount_usd: number | null
    as_of?: string
    unavailable_reason?: string
  }
  leg1_current_price: number
  leg2_current_price: number
  leg1_liq_price: number
  leg2_liq_price: number
  leg1_liq_dist: number
  leg2_liq_dist: number
  leg1_liq_risk: string
  leg2_liq_risk: string
  hold_hours: number
  started_at: string
  opened_at?: string
  completed_at?: string
  monitor_at?: string
  updated_at: string
}

interface PositionFeed {
  positions: LivePosition[]
  loaded: boolean
  error: string | null
  connected: boolean
  active: boolean
  controller: AbortController
  pending: Promise<void> | null
  queued: boolean
  version: string | null
  refresh: (queue?: boolean) => Promise<void>
  stop: () => void
}

function useLivePositionsState() {
  const [snapshots, setSnapshots] = useState<Record<string, Pick<PositionFeed, 'positions' | 'loaded' | 'error'>>>({})
  const feeds = useRef(new Map<string, PositionFeed>())
  const pageVisible = usePageVisibility()
  const visible = useRef(pageVisible)
  visible.current = pageVisible
  const { pacificaAddress, hyperliquidAddress, asterAddress, phoenixAddress } = useVenueAuthority()
  const accountPairs = useMemo(() => {
    const entries = Object.entries({
      pacifica: pacificaAddress,
      hyperliquid: hyperliquidAddress,
      aster: asterAddress,
      phoenix: phoenixAddress,
    }).filter((entry): entry is [string, string] => !!entry[1])
    const pairs: Record<string, string>[] = []
    for (let left = 0; left < entries.length; left++) {
      for (let right = left + 1; right < entries.length; right++) {
        pairs.push(Object.fromEntries([entries[left], entries[right]]))
      }
    }
    return pairs
  }, [asterAddress, hyperliquidAddress, pacificaAddress, phoenixAddress])
  // The provider owns one cache/request/transport lifetime per pair. Components
  // consume this state; mounting another panel cannot start another fetch loop.
  useEffect(() => {
    const current = feeds.current
    const publish = () => setSnapshots(Object.fromEntries([...current].map(([key, feed]) => [key,
      { positions: feed.positions, loaded: feed.loaded, error: feed.error },
    ])))
    const wanted = new Set(accountPairs.map(liveAccountsKey))
    for (const [key, feed] of current) {
      if (!wanted.has(key)) { feed.stop(); current.delete(key) }
    }
    for (const accounts of accountPairs) {
      const key = liveAccountsKey(accounts)
      if (current.has(key)) continue
      const feed: PositionFeed = {
        positions: [], loaded: false, error: null, connected: false, active: true,
        controller: new AbortController(), pending: null, queued: false, version: null,
        refresh: async () => {}, stop: () => {},
      }
      feed.refresh = (queue = false) => {
        if (!feed.active) return Promise.resolve()
        if (feed.pending) { feed.queued ||= queue; return feed.pending }
        feed.pending = (async () => {
          try {
            const data = await accountRead(async (signal) => {
              const resp = await apiFetch(`/api/v1/live/positions?${liveAccountsQuery(accounts)}`, { signal })
              if (!resp.ok) throw await apiResponseError(resp, 'Unable to load live positions. Please try again.')
              return resp.json() as Promise<LivePosition[]>
            }, feed.controller.signal)
            if (!feed.active) return
            feed.positions = data
            feed.loaded = true
            feed.error = null
          } catch (e) {
            if (!feed.active) return
            feed.error = userErrorMessage(e, 'Unable to load live positions. Please try again.')
          } finally {
            feed.pending = null
            if (feed.active) publish()
            if (feed.queued && feed.active) { feed.queued = false; void feed.refresh() }
          }
        })()
        return feed.pending
      }
      current.set(key, feed)
      const unsubscribe = subscribeLiveAccountEvents(accounts, (event) => {
        if (!feed.active) return
        if (event.type === 'connected') feed.connected = true
        else if (event.type === 'disconnected') {
          const wasConnected = feed.connected
          feed.connected = false
          if (wasConnected && shouldMonitorLiveUpdates(visible.current, feed.loaded ? hasActiveLiveExposure(feed.positions) : null)) void feed.refresh()
        }
        else if (event.type === 'positions') {
          const version = JSON.stringify(event.data)
          if (feed.version === version) return
          feed.version = version
          // Refresh only this pair; REST retains its funding enrichment.
          void feed.refresh(true)
        }
      })
      let timer = 0
      const schedule = () => {
        const active = feed.loaded ? hasActiveLiveExposure(feed.positions) : null
        timer = window.setTimeout(async () => {
          if ((!feed.connected || feed.error) && shouldMonitorLiveUpdates(visible.current, active)) await feed.refresh()
          if (feed.active) schedule()
        }, livePositionPollInterval(active, 5_000))
      }
      feed.stop = () => {
        feed.active = false
        feed.controller.abort()
        unsubscribe()
        window.clearTimeout(timer)
      }
      void feed.refresh()
      schedule()
    }
    queueMicrotask(publish)
  }, [accountPairs])

  useEffect(() => {
    const current = feeds.current
    return () => {
      for (const feed of current.values()) feed.stop()
      current.clear()
    }
  }, [])

  const refetch = useCallback(async () => {
    await Promise.all(accountPairs.map((accounts) => feeds.current.get(liveAccountsKey(accounts))?.refresh()))
  }, [accountPairs])
  return useMemo(() => {
    const selected = accountPairs.map((accounts) => snapshots[liveAccountsKey(accounts)])
    const positions = [...new Map(selected.flatMap((feed) => feed?.positions ?? []).map((p) => [p.id, p])).values()]
    positions.sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
    return { positions, loading: selected.some((feed) => !feed || (!feed.loaded && !feed.error)),
      error: selected.find((feed) => feed?.error)?.error ?? null, refetch }
  }, [accountPairs, refetch, snapshots])
}

const LivePositionsContext = createContext<ReturnType<typeof useLivePositionsState> | null>(null)

export function LivePositionsProvider({ children }: { children: ReactNode }) {
  const value = useLivePositionsState()
  return createElement(LivePositionsContext.Provider, { value }, children)
}

export function useLivePositions() {
  const value = useContext(LivePositionsContext)
  if (!value) throw new Error('useLivePositions must be used within LivePositionsProvider')
  return value
}
