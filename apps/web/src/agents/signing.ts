import type { SignedAction, SigningRequest } from '@/types/signing'
import { signHyperliquidAgentRequest } from './hyperliquid-agent.ts'
import { signPacificaAgentRequest } from './pacifica-agent.ts'
import { loadStoredTradingAgent, type StorageLike } from './storage.ts'

export async function signWithStoredTradingAgent(
  storage: StorageLike,
  request: SigningRequest,
): Promise<SignedAction> {
  const agent = loadStoredTradingAgent(storage, request.venue, request.account)
  if (!agent) throw new Error(`${venueName(request.venue)} authorization is not ready`)
  if (request.venue === 'pacifica') return signPacificaAgentRequest(request, agent)
  return signHyperliquidAgentRequest(request, agent)
}

function venueName(venue: SigningRequest['venue']): string {
  return venue === 'pacifica' ? 'Pacifica' : 'Hyperliquid'
}
