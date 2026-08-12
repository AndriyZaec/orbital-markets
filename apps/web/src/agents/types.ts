import type { SignedAction, SigningRequest } from '@/types/signing'

export type Venue = 'hyperliquid' | 'pacifica'

export interface StoredTradingAgent {
  version: 1
  venue: Venue
  ownerAddress: string
  agentAddress: string
  privateKey: string
  authorizedAt: string
}

export interface TradingAgentState {
  venue: Venue
  ownerAddress: string | null
  agentAddress: string | null
  status: 'missing' | 'authorizing' | 'ready' | 'error'
  error: string | null
}

export interface TradingAgentManager {
  hyperliquid: TradingAgentState
  pacifica: TradingAgentState
  authorize(venue: Venue): Promise<void>
  sign(request: SigningRequest): Promise<SignedAction>
  clear(venue: Venue): void
}
