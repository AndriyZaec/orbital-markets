import { type Address, type Hex } from 'viem'
import { generatePrivateKey, privateKeyToAccount } from 'viem/accounts'

import type { SignedAction, SigningRequest } from '@/types/signing'
import { saveStoredTradingAgent, type StorageLike } from './storage.ts'
import type { StoredTradingAgent } from './types'

const zeroAddress = '0x0000000000000000000000000000000000000000' as const
const ownerChainId = 56
const orderChainId = 1666
const agentName = 'Orbital Markets'
const agentLifetime = 7 * 24 * 60 * 60 * 1000

export interface AsterApproveAgentRequest {
  user: Address
  nonce: number
  signature: Hex
  agentName: typeof agentName
  agentAddress: Address
  expired: number
  canSpotTrade: false
  canPerpTrade: true
  canWithdraw: false
  asterChain: 'Mainnet'
  signatureChainId: typeof ownerChainId
}

type AsterApproveAgentAction = Omit<AsterApproveAgentRequest, 'signature'>

export function generateAsterAgent(): { privateKey: Hex; agentAddress: Address } {
  const privateKey = generatePrivateKey()
  return { privateKey, agentAddress: privateKeyToAccount(privateKey).address }
}

export function buildAsterApproveAgentAction(
  user: string,
  agentAddress: string,
  now: number,
): AsterApproveAgentAction {
  if (!isAddress(user) || !isAddress(agentAddress)) throw new Error('Invalid Aster owner or agent address')
  if (!Number.isSafeInteger(now) || now <= 0) throw new Error('Invalid Aster authorization time')
  return {
    user: user as Address,
    nonce: now * 1000,
    agentName,
    agentAddress: agentAddress as Address,
    expired: now + agentLifetime,
    canSpotTrade: false,
    canPerpTrade: true,
    canWithdraw: false,
    asterChain: 'Mainnet',
    signatureChainId: ownerChainId,
  }
}

export function buildAsterApproveAgentTypedData(action: AsterApproveAgentAction) {
  return {
    domain: {
      name: 'AsterSignTransaction',
      version: '1',
      chainId: ownerChainId,
      verifyingContract: zeroAddress,
    },
    types: {
      ApproveAgent: [
        { name: 'AgentName', type: 'string' },
        { name: 'AgentAddress', type: 'string' },
        { name: 'Expired', type: 'uint256' },
        { name: 'CanSpotTrade', type: 'bool' },
        { name: 'CanPerpTrade', type: 'bool' },
        { name: 'CanWithdraw', type: 'bool' },
        { name: 'AsterChain', type: 'string' },
        { name: 'User', type: 'string' },
        { name: 'Nonce', type: 'uint256' },
      ],
    },
    primaryType: 'ApproveAgent' as const,
    message: {
      AgentName: action.agentName,
      AgentAddress: action.agentAddress,
      Expired: BigInt(action.expired),
      CanSpotTrade: action.canSpotTrade,
      CanPerpTrade: action.canPerpTrade,
      CanWithdraw: action.canWithdraw,
      AsterChain: action.asterChain,
      User: action.user,
      Nonce: BigInt(action.nonce),
    },
  }
}

export async function authorizeAsterAgent(options: {
  storage: StorageLike
  ownerAddress: string
  signTypedData: (typedData: ReturnType<typeof buildAsterApproveAgentTypedData>) => Promise<Hex>
  relay: (request: AsterApproveAgentRequest) => Promise<void>
  ownerStillCurrent?: () => boolean
  now?: () => number
}): Promise<StoredTradingAgent> {
  const generated = generateAsterAgent()
  const action = buildAsterApproveAgentAction(
    options.ownerAddress,
    generated.agentAddress,
    options.now?.() ?? Date.now(),
  )
  const signature = await options.signTypedData(buildAsterApproveAgentTypedData(action))
  if (options.ownerStillCurrent && !options.ownerStillCurrent()) {
    throw new Error('Aster owner changed during agent authorization')
  }
  await options.relay({ ...action, signature })

  const agent: StoredTradingAgent = {
    version: 1,
    venue: 'aster',
    ownerAddress: options.ownerAddress,
    agentAddress: generated.agentAddress,
    privateKey: generated.privateKey,
    authorizedAt: new Date(Math.floor(action.nonce / 1000)).toISOString(),
    expiresAt: new Date(action.expired).toISOString(),
  }
  saveStoredTradingAgent(options.storage, agent)
  return agent
}

export async function signAsterAgentRequest(
  request: SigningRequest,
  agent: StoredTradingAgent,
): Promise<SignedAction> {
  const payload = allowedAsterOrder(request, agent)
  const account = privateKeyToAccount(agent.privateKey as Hex)
  if (account.address.toLowerCase() !== agent.agentAddress.toLowerCase()) {
    throw new Error('Aster agent key does not match its address')
  }
  const signature = await account.signTypedData({
    domain: payload.domain,
    types: { Message: [{ name: 'msg', type: 'string' }] },
    primaryType: 'Message',
    message: payload.message,
  })
  return {
    request_id: request.id,
    client_order_id: request.client_order_id,
    venue: 'aster',
    signer_address: agent.agentAddress,
    signature,
  }
}

interface AsterOrderPayload {
  domain: {
    name: 'AsterSignTransaction'
    version: '1'
    chainId: typeof orderChainId
    verifyingContract: typeof zeroAddress
  }
  message: { msg: string }
}

function allowedAsterOrder(request: SigningRequest, agent: StoredTradingAgent): AsterOrderPayload {
  const payload = request.unsigned_payload as Record<string, unknown> | null
  const domain = payload?.domain as Record<string, unknown> | undefined
  const types = payload?.types as Record<string, unknown> | undefined
  const message = payload?.message as Record<string, unknown> | undefined
  const msg = message?.msg
  const actionAllowed = request.action === 'open' || request.action === 'close' ||
    request.action === 'unwind' || request.action === 'emergency_close'
  const allowed =
    request.venue === 'aster' && agent.venue === 'aster' &&
    request.account.toLowerCase() === agent.ownerAddress.toLowerCase() &&
    request.signer?.toLowerCase() === agent.agentAddress.toLowerCase() &&
    !!agent.expiresAt && Date.parse(agent.expiresAt) > Date.now() &&
    Date.parse(request.expires_at) > Date.now() && actionAllowed &&
    (request.action === 'open' ? !request.reduce_only : request.reduce_only) &&
    hasOnlyKeys(payload, ['domain', 'types', 'primaryType', 'message']) &&
    payload?.primaryType === 'Message' &&
    hasOnlyKeys(domain, ['name', 'version', 'chainId', 'verifyingContract']) &&
    domain?.name === 'AsterSignTransaction' && domain.version === '1' &&
    domain.chainId === orderChainId && domain.verifyingContract === zeroAddress &&
    hasOnlyKeys(types, ['EIP712Domain', 'Message']) &&
    isAsterDomainType(types?.EIP712Domain) && isAsterMessageType(types?.Message) &&
    hasOnlyKeys(message, ['msg']) && typeof msg === 'string'
  if (!allowed) throw new Error('Aster payload is not an allowed IOC order')

  const query = parseUniqueQuery(msg)
  const expectedKeys = [
    'symbol', 'type', 'side', 'quantity', 'price', 'timeInForce',
    'newClientOrderId', 'newOrderRespType', 'reduceOnly', 'positionSide',
    'asterChain', 'user', 'signer', 'nonce',
  ]
  const quantity = Number(query.get('quantity'))
  const price = Number(query.get('price'))
  const nonce = Number(query.get('nonce'))
  const validQuery =
    query.size === expectedKeys.length && expectedKeys.every((key) => query.has(key)) &&
    query.get('symbol') === request.symbol && query.get('type') === 'LIMIT' &&
    query.get('side') === request.side.toUpperCase() &&
    Number.isFinite(quantity) && quantity > 0 && quantity === request.amount &&
    Number.isFinite(price) && price > 0 && price === request.price &&
    query.get('timeInForce') === 'IOC' &&
    query.get('newClientOrderId') === request.client_order_id &&
    query.get('newOrderRespType') === 'RESULT' &&
    query.get('reduceOnly') === String(request.reduce_only) &&
    query.get('positionSide') === 'BOTH' && query.get('asterChain') === 'Mainnet' &&
    query.get('user')?.toLowerCase() === request.account.toLowerCase() &&
    query.get('signer')?.toLowerCase() === agent.agentAddress.toLowerCase() &&
    Number.isSafeInteger(nonce) && nonce > 0
  if (!validQuery) throw new Error('Aster payload is not an allowed IOC order')

  return {
    domain: {
      name: 'AsterSignTransaction', version: '1', chainId: orderChainId,
      verifyingContract: zeroAddress,
    },
    message: { msg },
  }
}

function parseUniqueQuery(value: string): Map<string, string> {
  const query = new Map<string, string>()
  for (const part of value.split('&')) {
    const separator = part.indexOf('=')
    if (separator <= 0) throw new Error('Aster payload contains an invalid query')
    let key: string
    let fieldValue: string
    try {
      key = decodeURIComponent(part.slice(0, separator).replaceAll('+', ' '))
      fieldValue = decodeURIComponent(part.slice(separator + 1).replaceAll('+', ' '))
    } catch {
      throw new Error('Aster payload contains an invalid query')
    }
    if (query.has(key)) throw new Error('Aster payload contains duplicate query parameters')
    query.set(key, fieldValue)
  }
  return query
}

function isAsterDomainType(value: unknown): boolean {
  return JSON.stringify(value) === JSON.stringify([
    { name: 'name', type: 'string' },
    { name: 'version', type: 'string' },
    { name: 'chainId', type: 'uint256' },
    { name: 'verifyingContract', type: 'address' },
  ])
}

function isAsterMessageType(value: unknown): boolean {
  return JSON.stringify(value) === JSON.stringify([{ name: 'msg', type: 'string' }])
}

function hasOnlyKeys(value: object | null | undefined, keys: string[]): boolean {
  if (!value) return false
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  return actual.length === expected.length && actual.every((key, index) => key === expected[index])
}

function isAddress(value: string): boolean {
  return /^0x[0-9a-fA-F]{40}$/.test(value)
}
