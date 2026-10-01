const ACTIVE_STATES = new Set(['pending', 'open', 'degraded', 'closing'])
const OPEN_STATES = new Set(['pending', 'opening', 'open', 'monitoring', 'closing'])
const DEGRADED_STATES = new Set(['degraded', 'broken_hedge', 'partial', 'stuck', 'error'])
const CLOSED_STATES = new Set(['closed', 'failed'])

export type PortfolioPositionCategory = 'open' | 'degraded' | 'closed'

export function portfolioPositionCategory(state: string, hedgeMismatch: number): PortfolioPositionCategory {
  const normalizedState = state.toLowerCase()
  if (DEGRADED_STATES.has(normalizedState)) return 'degraded'
  if (OPEN_STATES.has(normalizedState)) return hedgeMismatch > 0.01 ? 'degraded' : 'open'
  return 'closed'
}

export function isActivePositionState(state: string): boolean {
  return ACTIVE_STATES.has(state.toLowerCase())
}

export function isClosedPositionState(state: string): boolean {
  return CLOSED_STATES.has(state.toLowerCase())
}
