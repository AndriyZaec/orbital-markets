import { createContext, useContext } from 'react'

import type { TradingAgentManager } from './types'

export const TradingAgentContext = createContext<TradingAgentManager | null>(null)

export function useTradingAgentManager(): TradingAgentManager {
  const manager = useContext(TradingAgentContext)
  if (!manager) throw new Error('useTradingAgents must be used within TradingAgentProvider')
  return manager
}
