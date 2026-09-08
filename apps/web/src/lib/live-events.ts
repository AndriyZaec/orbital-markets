import { apiUrl } from './api.ts'
import { liveAccountsKey, liveAccountsQuery, type VenueAddressMap } from './live-bindings.ts'

export type LiveAccountEvent =
  | { type: 'connected' | 'disconnected' }
  | { type: 'balances' | 'positions'; data: unknown }

interface AccountChannel {
  source: EventSource
  listeners: Set<(event: LiveAccountEvent) => void>
  connected: boolean
}

const accountChannels = new Map<string, AccountChannel>()

export function hasActiveLiveExposure(data: unknown): boolean {
  return Array.isArray(data) && data.some((position) => {
    if (!position || typeof position !== 'object' || !('state' in position)) return false
    return position.state === 'pending' || position.state === 'open' || position.state === 'degraded' || position.state === 'closing'
  })
}

function liveEventsUrl(accounts: VenueAddressMap, sessionId?: string) {
  const query = liveAccountsQuery(accounts, sessionId ? { session_id: sessionId } : {})
  return apiUrl(`/api/v1/live/events?${query}`)
}

function liveAccountEventsUrl(accounts: VenueAddressMap) {
  return apiUrl(`/api/v1/live/accounts/events?${liveAccountsQuery(accounts)}`)
}

export function subscribeLiveAccountEvents(
  accounts: VenueAddressMap,
  listener: (event: LiveAccountEvent) => void,
): () => void {
  return subscribeLiveEventSource(
    `pair:${liveAccountsKey(accounts)}`,
    liveEventsUrl(accounts),
    ['balances', 'positions'],
    listener,
  )
}

function subscribeLiveEventSource(
  key: string,
  url: string,
  eventTypes: Array<'balances' | 'positions'>,
  listener: (event: LiveAccountEvent) => void,
): () => void {
  let channel = accountChannels.get(key)
  if (!channel) {
    const source = new EventSource(url, { withCredentials: true })
    channel = { source, listeners: new Set(), connected: false }
    accountChannels.set(key, channel)
    const dispatch = (event: LiveAccountEvent) => {
      for (const current of channel!.listeners) current(event)
    }
    source.onopen = () => {
      channel!.connected = true
      dispatch({ type: 'connected' })
    }
    source.onerror = () => {
      channel!.connected = false
      dispatch({ type: 'disconnected' })
    }
    for (const type of eventTypes) {
      source.addEventListener(type, (message) => {
        try {
          dispatch({ type, data: JSON.parse((message as MessageEvent).data) })
        } catch {
          // A malformed event is ignored; the next full snapshot repairs state.
        }
      })
    }
  }
  channel.listeners.add(listener)
  if (channel.connected) listener({ type: 'connected' })

  return () => {
    channel!.listeners.delete(listener)
    if (channel!.listeners.size === 0) {
      channel!.source.close()
      accountChannels.delete(key)
    }
  }
}

export function subscribeLiveAccountUpdates(
  accounts: VenueAddressMap,
  listener: (event: LiveAccountEvent) => void,
): () => void {
  return subscribeLiveEventSource(
    `accounts:${liveAccountsKey(accounts)}`,
    liveAccountEventsUrl(accounts),
    ['balances'],
    listener,
  )
}

export function subscribeLiveSessionEvents(
  accounts: VenueAddressMap,
  sessionId: string,
  onConnection: (connected: boolean) => void,
  onSession: (data: unknown) => void,
): () => void {
  const source = new EventSource(liveEventsUrl(accounts, sessionId), {
    withCredentials: true,
  })
  source.onopen = () => onConnection(true)
  source.onerror = () => onConnection(false)
  source.addEventListener('session', (message) => {
    try {
      onSession(JSON.parse((message as MessageEvent).data))
    } catch {
      // A malformed event is ignored; reconnect and REST fallback remain active.
    }
  })
  return () => source.close()
}
