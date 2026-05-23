import type { SigningRequest, SignedAction } from '@/types/signing'
import { signPacifica } from './pacifica'
import { signHyperliquid } from './hyperliquid'
import type { SignTypedDataParameters } from 'wagmi/actions'

export interface Signers {
  pacifica: {
    signMessage: (message: Uint8Array) => Promise<Uint8Array>
    publicKey: string
  } | null
  hyperliquid: {
    signTypedDataAsync: (params: {
      domain: SignTypedDataParameters['domain']
      types: SignTypedDataParameters['types']
      primaryType: string
      message: Record<string, unknown>
    }) => Promise<string>
    address: string
  } | null
}

export async function signRequest(
  request: SigningRequest,
  signers: Signers,
): Promise<SignedAction> {
  if (request.venue === 'pacifica') {
    if (!signers.pacifica) throw new Error('Pacifica signer not available')
    const signature = await signPacifica(request.unsigned_payload, signers.pacifica.signMessage)
    return {
      request_id: request.id,
      client_order_id: request.client_order_id,
      venue: request.venue,
      signer_address: signers.pacifica.publicKey,
      signature,
    }
  }

  if (request.venue === 'hyperliquid') {
    if (!signers.hyperliquid) throw new Error('Hyperliquid signer not available')
    const signature = await signHyperliquid(request.unsigned_payload, signers.hyperliquid.signTypedDataAsync)
    return {
      request_id: request.id,
      client_order_id: request.client_order_id,
      venue: request.venue,
      signer_address: signers.hyperliquid.address,
      signature,
    }
  }

  throw new Error(`Unknown venue: ${request.venue}`)
}
