import type { SignedAction, SigningRequest } from '@/types/signing'
import { signAsterAgentRequest } from './aster-agent.ts'
import { signHyperliquidAgentRequest } from './hyperliquid-agent.ts'
import { signPacificaAgentRequest } from './pacifica-agent.ts'
import type { TradingAgentStore } from './storage.ts'

export async function signWithStoredTradingAgent(
  storage: TradingAgentStore,
  request: SigningRequest,
): Promise<SignedAction> {
  assertSupportedSigningVenue(request.venue)
  const agent = await storage.loadForSigning(request.venue, request.account)
  if (!agent) throw new Error(`${venueName(request.venue)} authorization is not ready`)
  if (request.venue === 'pacifica') return signPacificaAgentRequest(request, agent)
  if (request.venue === 'hyperliquid') return signHyperliquidAgentRequest(request, agent)
  return signAsterAgentRequest(request, agent)
}

export function assertSupportedSigningVenue(
  venue: unknown,
): asserts venue is SigningRequest['venue'] {
  if (venue !== 'pacifica' && venue !== 'hyperliquid' && venue !== 'aster') {
    throw new Error(`Unsupported signing venue: ${String(venue)}`)
  }
}

function venueName(venue: SigningRequest['venue']): string {
  if (venue === 'pacifica') return 'Pacifica'
  return venue === 'hyperliquid' ? 'Hyperliquid' : 'Aster'
}
