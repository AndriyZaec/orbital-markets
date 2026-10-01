import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { apiFetch, apiResponseError, userErrorMessage } from '@/lib/api'
import { liveAccountsKey, type VenueAddressMap } from '@/lib/live-bindings'
import { subscribeLiveAccountEvents } from '@/lib/live-events'
import { useVenueAuthority } from './useVenueAuthority'
import type { LivePosition } from './useLivePositions'

export type LiveActivityFilter = 'all' | 'opened' | 'closed'

export interface LiveActivityItem {
  id: string
  type: string
  at: string
  position: LivePosition
}

interface LiveActivityResponse {
  items: LiveActivityItem[]
  next_cursor?: string
}

export function useLiveActivity(filter: LiveActivityFilter) {
  const { pacificaAddress, hyperliquidAddress, asterAddress } = useVenueAuthority()
  const accounts = useMemo<VenueAddressMap>(() => Object.fromEntries(Object.entries({
    pacifica: pacificaAddress,
    hyperliquid: hyperliquidAddress,
    aster: asterAddress,
  }).filter((entry): entry is [string, string] => Boolean(entry[1]))), [asterAddress, hyperliquidAddress, pacificaAddress])
  const accountKey = liveAccountsKey(accounts)
  const accountPairs = useMemo(() => {
    const entries = Object.entries(accounts)
    const pairs: VenueAddressMap[] = []
    for (let left = 0; left < entries.length; left++) {
      for (let right = left + 1; right < entries.length; right++) {
        pairs.push(Object.fromEntries([entries[left], entries[right]]))
      }
    }
    return pairs
  }, [accounts])
  const [state, setState] = useState<{
    key: string
    items: LiveActivityItem[]
    nextCursor: string | null
    loading: boolean
    loadingMore: boolean
    error: string | null
  }>({ key: '', items: [], nextCursor: null, loading: false, loadingMore: false, error: null })
  const requestRef = useRef(0)
  const refreshTimer = useRef<number | null>(null)
  const key = `${accountKey}|${filter}`

  const fetchPage = useCallback(async (cursor?: string, replace = false) => {
    if (Object.keys(accounts).length < 2) {
      setState({ key, items: [], nextCursor: null, loading: false, loadingMore: false, error: null })
      return
    }
    const request = ++requestRef.current
    setState((current) => ({
      ...(current.key === key ? current : { key, items: [], nextCursor: null, error: null }),
      key,
      loading: !cursor,
      loadingMore: Boolean(cursor),
      error: null,
    }))
    try {
      // Activity accepts the canonical map only; unlike older live endpoints,
      // sending legacy aliases as well is intentionally treated as a duplicate.
      const query = new URLSearchParams({ limit: '20' })
      for (const [venue, account] of Object.entries(accounts)) query.set(`accounts[${venue}]`, account)
      if (filter !== 'all') query.append('type', filter)
      if (cursor) query.set('cursor', cursor)
      const response = await apiFetch(`/api/v1/live/activity?${query}`)
      if (!response.ok) throw await apiResponseError(response, 'Unable to load recent activity.')
      const data = await response.json() as LiveActivityResponse
      if (request !== requestRef.current) return
      setState((current) => {
        const existing = replace || !cursor || current.key !== key ? [] : current.items
        const items = [...new Map([...existing, ...data.items].map((item) => [item.id, item])).values()]
        return { key, items, nextCursor: data.next_cursor ?? null, loading: false, loadingMore: false, error: null }
      })
    } catch (error) {
      if (request !== requestRef.current) return
      setState((current) => ({
        ...(current.key === key ? current : { key, items: [], nextCursor: null }),
        loading: false,
        loadingMore: false,
        error: userErrorMessage(error, 'Unable to load recent activity.'),
      }))
    }
  }, [accounts, filter, key])

  useEffect(() => {
    void fetchPage(undefined, true)
  }, [fetchPage])

  useEffect(() => {
    if (accountPairs.length === 0) return
    const subscriptions = accountPairs.map((pair) => subscribeLiveAccountEvents(pair, (event) => {
      if (event.type !== 'positions') return
      if (refreshTimer.current !== null) window.clearTimeout(refreshTimer.current)
      refreshTimer.current = window.setTimeout(() => void fetchPage(undefined, true), 250)
    }))
    return () => {
      subscriptions.forEach((unsubscribe) => unsubscribe())
      if (refreshTimer.current !== null) {
        window.clearTimeout(refreshTimer.current)
        refreshTimer.current = null
      }
    }
  }, [accountKey, accountPairs, fetchPage])

  useEffect(() => () => {
    if (refreshTimer.current !== null) window.clearTimeout(refreshTimer.current)
  }, [])

  const current = state.key === key
    ? state
    : { items: [], nextCursor: null, loading: Object.keys(accounts).length >= 2, loadingMore: false, error: null }

  return {
    ...current,
    loadMore: () => current.nextCursor ? fetchPage(current.nextCursor) : Promise.resolve(),
    refetch: () => fetchPage(undefined, true),
  }
}
