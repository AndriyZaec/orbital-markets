const OPEN_STATES = new Set(['opening', 'open', 'monitoring', 'closing'])
const DEGRADED_STATES = new Set(['degraded', 'broken_hedge', 'partial', 'stuck', 'error'])

export type PortfolioPositionCategory = 'open' | 'degraded' | 'closed'

export function portfolioPositionCategory(state: string, hedgeMismatch: number): PortfolioPositionCategory {
  const normalizedState = state.toLowerCase()
  if (DEGRADED_STATES.has(normalizedState)) return 'degraded'
  if (OPEN_STATES.has(normalizedState)) return hedgeMismatch > 0.01 ? 'degraded' : 'open'
  return 'closed'
}
