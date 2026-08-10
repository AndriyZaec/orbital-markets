import { toHex, type Address, type Hex } from 'viem'
import { generatePrivateKey, privateKeyToAccount } from 'viem/accounts'

import type { SignedAction, SigningRequest } from '@/types/signing'
import { saveStoredTradingAgent, type StorageLike } from './storage.ts'
import type { StoredTradingAgent } from './types'

const zeroAddress = '0x0000000000000000000000000000000000000000' as const
const agentName = 'Orbital Markets'

export interface HyperliquidApproveAgentAction {
  [key: string]: unknown
  type: 'approveAgent'
  hyperliquidChain: 'Mainnet'
  signatureChainId: Hex
  agentAddress: Address
  agentName: string
  nonce: number
}

export interface HyperliquidApproveAgentRequest {
  action: HyperliquidApproveAgentAction
  signature: { r: Hex; s: Hex; v: 27 | 28 }
}

export function generateHyperliquidAgent(): { privateKey: Hex; agentAddress: Address } {
  const privateKey = generatePrivateKey()
  return { privateKey, agentAddress: privateKeyToAccount(privateKey).address }
}

export function buildHyperliquidApproveAgentAction(
  agentAddress: string,
  chainId: number,
  nonce: number,
): HyperliquidApproveAgentAction {
  if (!/^0x[0-9a-fA-F]{40}$/.test(agentAddress)) throw new Error('Invalid Hyperliquid agent address')
  if (!Number.isSafeInteger(chainId) || chainId <= 0) throw new Error('Invalid wallet chain ID')
  if (!Number.isSafeInteger(nonce) || nonce <= 0) throw new Error('Invalid authorization nonce')
  return {
    type: 'approveAgent',
    hyperliquidChain: 'Mainnet',
    signatureChainId: toHex(chainId),
    agentAddress: agentAddress as Address,
    agentName,
    nonce,
  }
}

export function buildHyperliquidApproveAgentTypedData(action: HyperliquidApproveAgentAction) {
  return {
    domain: {
      name: 'HyperliquidSignTransaction',
      version: '1',
      chainId: Number.parseInt(action.signatureChainId, 16),
      verifyingContract: zeroAddress,
    },
    types: {
      'HyperliquidTransaction:ApproveAgent': [
        { name: 'hyperliquidChain', type: 'string' },
        { name: 'agentAddress', type: 'address' },
        { name: 'agentName', type: 'string' },
        { name: 'nonce', type: 'uint64' },
      ] as const,
    },
    primaryType: 'HyperliquidTransaction:ApproveAgent' as const,
    message: { ...action, nonce: BigInt(action.nonce) },
  }
}

export async function authorizeHyperliquidAgent(options: {
  storage: StorageLike
  ownerAddress: string
  chainId: number
  signTypedData: (typedData: ReturnType<typeof buildHyperliquidApproveAgentTypedData>) => Promise<Hex>
  relay: (request: HyperliquidApproveAgentRequest) => Promise<void>
  now?: () => number
}): Promise<StoredTradingAgent> {
  const now = options.now?.() ?? Date.now()
  const generated = generateHyperliquidAgent()
  const action = buildHyperliquidApproveAgentAction(generated.agentAddress, options.chainId, now)
  const ownerSignature = await options.signTypedData(buildHyperliquidApproveAgentTypedData(action))
  await options.relay({ action, signature: splitEthereumSignature(ownerSignature) })

  const agent: StoredTradingAgent = {
    version: 1,
    venue: 'hyperliquid',
    ownerAddress: options.ownerAddress,
    agentAddress: generated.agentAddress,
    privateKey: generated.privateKey,
    authorizedAt: new Date(now).toISOString(),
  }
  saveStoredTradingAgent(options.storage, agent)
  return agent
}

function splitEthereumSignature(signature: Hex): { r: Hex; s: Hex; v: 27 | 28 } {
  if (!/^0x[0-9a-fA-F]{130}$/.test(signature)) throw new Error('Invalid owner signature')
  const recovery = Number.parseInt(signature.slice(130), 16)
  const v = recovery === 0 || recovery === 1 ? recovery + 27 : recovery
  if (v !== 27 && v !== 28) throw new Error('Invalid owner signature recovery value')
  return {
    r: `0x${signature.slice(2, 66)}`,
    s: `0x${signature.slice(66, 130)}`,
    v,
  }
}

export async function signHyperliquidAgentRequest(
  request: SigningRequest,
  agent: StoredTradingAgent,
): Promise<SignedAction> {
  const payload = allowedL1OrderPayload(request, agent)
  const account = privateKeyToAccount(agent.privateKey as Hex)
  if (account.address.toLowerCase() !== agent.agentAddress.toLowerCase()) {
    throw new Error('Hyperliquid agent key does not match its address')
  }

  const signature = await account.signTypedData({
    domain: payload.domain,
    types: {
      Agent: [
        { name: 'source', type: 'string' },
        { name: 'connectionId', type: 'bytes32' },
      ],
    },
    primaryType: 'Agent',
    message: payload.message,
  })
  return {
    request_id: request.id,
    client_order_id: request.client_order_id,
    venue: request.venue,
    signer_address: agent.agentAddress,
    signature,
  }
}

interface L1OrderPayload {
  domain: {
    chainId: 1337
    name: 'Exchange'
    verifyingContract: typeof zeroAddress
    version: '1'
  }
  message: { source: 'a'; connectionId: Hex }
}

function allowedL1OrderPayload(request: SigningRequest, agent: StoredTradingAgent): L1OrderPayload {
  const payload = request.unsigned_payload as Record<string, unknown> | null
  const domain = payload?.domain as Record<string, unknown> | undefined
  const message = payload?.message as Record<string, unknown> | undefined
  const action = payload?.action as Record<string, unknown> | undefined
  const types = payload?.types as Record<string, unknown> | undefined
  const agentType = types?.Agent
  const allowed =
    request.venue === 'hyperliquid' &&
    agent.venue === 'hyperliquid' &&
    request.account.toLowerCase() === agent.ownerAddress.toLowerCase() &&
    request.signer?.toLowerCase() === agent.agentAddress.toLowerCase() &&
    (request.action === 'open' || request.action === 'close') &&
    Date.parse(request.expires_at) > Date.now() &&
    action?.type === 'order' &&
    payload?.primaryType === 'Agent' &&
    domain?.chainId === 1337 &&
    domain?.name === 'Exchange' &&
    domain?.version === '1' &&
    domain?.verifyingContract === zeroAddress &&
    Array.isArray(agentType) &&
    agentType.length === 2 &&
    message?.source === 'a' &&
    typeof message.connectionId === 'string' &&
    /^0x[0-9a-fA-F]{64}$/.test(message.connectionId)
  if (!allowed) throw new Error('Hyperliquid payload is not an allowed L1 order')

  return {
    domain: {
      chainId: 1337,
      name: 'Exchange',
      verifyingContract: zeroAddress,
      version: '1',
    },
    message: { source: 'a', connectionId: message.connectionId as Hex },
  }
}
