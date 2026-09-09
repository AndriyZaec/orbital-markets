import { useEffect, useEffectEvent, type ReactNode } from 'react'

import { useTradingAgentManager } from './TradingAgentContext'

const streamRetryDelayMs = 30_000
const snapshotHeartbeatMs = 60_000
const leverageBracketsRefreshMs = 4 * 60_000

export function AsterAccountBridge({ children }: { children: ReactNode }) {
  const manager = useTradingAgentManager()
  const refreshAccount = useEffectEvent(() => manager.refreshAsterAccount())
  const startUserStream = useEffectEvent(() => manager.requestAster<{ listenKey: string }>({
    operation: 'start_user_stream',
  }))
  const keepaliveUserStream = useEffectEvent(() => manager.requestAster({
    operation: 'keepalive_user_stream',
  }))
  const refreshLeverageBrackets = useEffectEvent(() => manager.requestAster({
    operation: 'get_leverage_brackets',
  }))
  const closeUserStream = useEffectEvent(() => manager.requestAster({
    operation: 'close_user_stream',
  }))

  useEffect(() => {
    if (manager.aster.status !== 'ready' || !manager.aster.ownerAddress || !manager.aster.agentAddress) return

    let active = true
    let socket: WebSocket | null = null
    let refreshTimer = 0
    let reconnectTimer = 0
    let refreshRunning = false
    let refreshQueued = false
    let lastRefreshAt = 0
    let streamStarted = false
    let streamStarting = false
    let streamRetryAvailable = true
    let accountUnavailable = false
    let leverageBracketsUpdatedAt = 0
    let leverageBracketsAttemptedAt = 0

    const retryStreamOnce = (retry: () => void) => {
      if (!active || accountUnavailable || !streamRetryAvailable) return
      streamRetryAvailable = false
      window.clearTimeout(reconnectTimer)
      reconnectTimer = window.setTimeout(retry, streamRetryDelayMs)
    }

    const refresh = async (): Promise<boolean> => {
      window.clearTimeout(refreshTimer)
      refreshTimer = 0
      if (refreshRunning) {
        refreshQueued = true
        return false
      }
      refreshRunning = true
      refreshQueued = false
      lastRefreshAt = Date.now()
      try {
        const status = await refreshAccount()
        if (status === 'deposit_required') {
          accountUnavailable = true
          streamRetryAvailable = false
          streamStarted = false
          window.clearTimeout(reconnectTimer)
          const current = socket
          socket = null
          current?.close()
        } else {
          const recovered = accountUnavailable
          accountUnavailable = false
          streamRetryAvailable = true
          if (Date.now() - leverageBracketsUpdatedAt >= leverageBracketsRefreshMs &&
            Date.now() - leverageBracketsAttemptedAt >= streamRetryDelayMs) {
            leverageBracketsAttemptedAt = Date.now()
            try {
              await refreshLeverageBrackets()
              leverageBracketsUpdatedAt = Date.now()
            } catch {
              // A later account refresh retries bracket discovery.
            }
          }
          if (recovered && active) void start()
        }
        return true
      } catch {
        // The next stream event or heartbeat retries the complete snapshot.
        return false
      } finally {
        refreshRunning = false
        if (active && refreshQueued) scheduleRefresh()
      }
    }
    const scheduleRefresh = () => {
      window.clearTimeout(refreshTimer)
      const minDelay = Math.max(250, 2_000 - (Date.now() - lastRefreshAt))
      refreshTimer = window.setTimeout(() => void refresh(), minDelay)
    }
    const connectSocket = (listenKey: string) => {
      if (!active || accountUnavailable) return
      const next = new WebSocket(`wss://fstream.asterdex.com/ws/${encodeURIComponent(listenKey)}`)
      socket = next
      next.onopen = () => {
        streamRetryAvailable = true
        scheduleRefresh()
      }
      next.onmessage = (event) => {
        try {
          const payload = JSON.parse(String(event.data)) as { e?: string }
          if (payload.e === 'listenKeyExpired') {
            socket = null
            next.close()
            void start()
            return
          }
        } catch {
          // Unknown payloads still trigger an authoritative REST refresh.
        }
        scheduleRefresh()
      }
      next.onerror = () => next.close()
      next.onclose = () => {
        if (!active || socket !== next) return
        retryStreamOnce(() => connectSocket(listenKey))
      }
    }
    const start = async () => {
      if (accountUnavailable || streamStarting) return
      streamStarting = true
      try {
        const result = await startUserStream()
        if (!active) {
          void closeUserStream().catch(() => {})
          return
        }
        if (!result.listenKey) throw new Error('Aster user stream returned no listen key')
        streamStarted = true
        connectSocket(result.listenKey)
      } catch {
        retryStreamOnce(() => void start())
      } finally {
        streamStarting = false
      }
    }

    // Delaying one tick prevents React StrictMode's probe mount from opening a duplicate stream.
    const startTimer = window.setTimeout(() => {
      void refresh().then((refreshed) => {
        if (refreshed && !accountUnavailable) void start()
      })
    }, 0)
    const snapshotHeartbeat = window.setInterval(scheduleRefresh, snapshotHeartbeatMs)
    const keepalive = window.setInterval(() => {
      if (accountUnavailable) return
      void keepaliveUserStream().catch(() => {
        const current = socket
        socket = null
        current?.close()
        void start()
      })
    }, 25 * 60_000)
    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') scheduleRefresh()
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      active = false
      document.removeEventListener('visibilitychange', onVisibilityChange)
      window.clearTimeout(startTimer)
      window.clearTimeout(refreshTimer)
      window.clearTimeout(reconnectTimer)
      window.clearInterval(snapshotHeartbeat)
      window.clearInterval(keepalive)
      const current = socket
      socket = null
      current?.close()
      if (streamStarted && !accountUnavailable) void closeUserStream().catch(() => {})
    }
  }, [manager.aster.agentAddress, manager.aster.ownerAddress, manager.aster.status])

  return children
}
