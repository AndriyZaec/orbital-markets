import { useEffect, useEffectEvent, type ReactNode } from 'react'

import { useTradingAgentManager } from './TradingAgentContext'

export function AsterAccountBridge({ children }: { children: ReactNode }) {
  const manager = useTradingAgentManager()
  const refreshAccount = useEffectEvent(() => manager.refreshAsterAccount())
  const startUserStream = useEffectEvent(() => manager.requestAster<{ listenKey: string }>({
    operation: 'start_user_stream',
  }))
  const keepaliveUserStream = useEffectEvent(() => manager.requestAster({
    operation: 'keepalive_user_stream',
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

    const refresh = async () => {
      window.clearTimeout(refreshTimer)
      refreshTimer = 0
      if (refreshRunning) {
        refreshQueued = true
        return
      }
      refreshRunning = true
      refreshQueued = false
      lastRefreshAt = Date.now()
      try {
        await refreshAccount()
      } catch {
        // The next stream event or heartbeat retries the complete snapshot.
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
      if (!active) return
      const next = new WebSocket(`wss://fstream.asterdex.com/ws/${encodeURIComponent(listenKey)}`)
      socket = next
      next.onopen = scheduleRefresh
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
        reconnectTimer = window.setTimeout(() => connectSocket(listenKey), 2_000)
      }
    }
    const start = async () => {
      if (streamStarting) return
      streamStarting = true
      try {
        const result = await startUserStream()
        streamStarted = true
        if (!active) {
          void closeUserStream().catch(() => {})
          return
        }
        if (!result.listenKey) throw new Error('Aster user stream returned no listen key')
        connectSocket(result.listenKey)
      } catch {
        if (active) reconnectTimer = window.setTimeout(() => void start(), 2_000)
      } finally {
        streamStarting = false
      }
    }

    // Delaying one tick prevents React StrictMode's probe mount from opening a duplicate stream.
    const startTimer = window.setTimeout(() => {
      void refresh()
      void start()
    }, 0)
    const snapshotHeartbeat = window.setInterval(scheduleRefresh, 10_000)
    const keepalive = window.setInterval(() => {
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
      if (streamStarted) void closeUserStream().catch(() => {})
    }
  }, [manager.aster.agentAddress, manager.aster.ownerAddress, manager.aster.status])

  return children
}
