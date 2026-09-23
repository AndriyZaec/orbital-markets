import type { SignedAction, SigningRequest } from '@/types/signing'

export type Venue = 'hyperliquid' | 'pacifica' | 'aster'
export type WalletKind = 'evm' | 'solana'

export interface StoredTradingAgent {
  version: 2
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
  status: 'missing' | 'restoring' | 'authorizing' | 'disconnecting' | 'ready' | 'error'
  error: string | null
  authorizationStep?: 1 | 2 | 3
}

export interface TradingAgentManager {
  hyperliquid: TradingAgentState
  pacifica: TradingAgentState
  aster: TradingAgentState
  authorize(venue: Venue): Promise<void>
  sign(request: SigningRequest): Promise<SignedAction>
  disconnectWallet(wallet: WalletKind): Promise<void>
}
