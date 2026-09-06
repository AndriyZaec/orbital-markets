import { useEffect, useEffectEvent, type ReactNode } from 'react'

import { useTradingAgentManager } from './TradingAgentContext'

export function AsterAccountBridge({ children }: { children: ReactNode }) {
  const manager = useTradingAgentManager()
  const refreshAccount = useEffectEvent(() => manager.refreshAsterAccount())

  useEffect(() => {
    if (manager.aster.status !== 'ready' || !manager.aster.ownerAddress || !manager.aster.agentAddress) return
    const refresh = async () => {
      try {
        await refreshAccount()
      } catch {
        // Readiness remains unavailable until a complete browser-signed refresh succeeds.
      }
    }
    void refresh()
  }, [manager.aster.agentAddress, manager.aster.ownerAddress, manager.aster.status])

  return children
}
