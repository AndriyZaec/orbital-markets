import type { SignedAction, SigningRequest } from '@/types/signing'

export type Venue = 'hyperliquid' | 'pacifica' | 'aster' | 'phoenix'

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
  phoenixOperation?: PhoenixOperation
}

export interface PhoenixOperation {
  requestID: string
  action: 'delegate' | 'revoke'
  targetAuthority: string
  submissionStarted: boolean
}

export interface TradingAgentState {
  venue: Venue
  ownerAddress: string | null
  agentAddress: string | null
  status: 'missing' | 'restoring' | 'authorizing' | 'disconnecting' | 'ready' | 'error' | 'pending'
  error: string | null
  authorizationStep?: 1 | 2 | 3
}

export interface TradingAgentManager {
  hyperliquid: TradingAgentState
  pacifica: TradingAgentState
  aster: TradingAgentState
  phoenix: TradingAgentState
  phoenixDisconnectNoticeOwner: string | null
  authorize(venue: Venue, options?: { replaceExistingDelegate?: boolean; expectedOwner?: string; expectedAuthority?: string }): Promise<void>
  revokePhoenix(): Promise<void>
  ensureHyperliquidTradingApproval(account: string): Promise<void>
  sign(request: SigningRequest): Promise<SignedAction>
  disconnectVenue(venue: Venue): Promise<void>
}
