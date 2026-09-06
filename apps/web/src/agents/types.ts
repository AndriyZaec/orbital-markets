import type { SignedAction, SigningRequest } from '@/types/signing'
import type { AsterPrivateInput } from './aster-private'

export type Venue = 'hyperliquid' | 'pacifica' | 'aster'

export interface StoredTradingAgent {
  version: 1
  venue: Venue
  ownerAddress: string
  agentAddress: string
  privateKey: string
  authorizedAt: string
  expiresAt?: string
  builderAddress?: string
  builderCode?: string
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
  aster: TradingAgentState
  authorize(venue: Venue): Promise<void>
  sign(request: SigningRequest): Promise<SignedAction>
  requestAster<T>(input: Omit<AsterPrivateInput, 'account' | 'agent'>): Promise<T>
  refreshAsterAccount(): Promise<void>
  clear(venue: Venue): void
}
