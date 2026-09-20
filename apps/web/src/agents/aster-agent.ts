import { recoverTypedDataAddress, type Address, type Hex } from 'viem'
import { generatePrivateKey, privateKeyToAccount } from 'viem/accounts'
import builderConfig from '../../../api/internal/venue/hyperliquid/live/builder_config.json' with { type: 'json' }

import type { SignedAction, SigningRequest } from '@/types/signing'
import {
  buildAsterDataAgentApprovalTypedData,
  type AsterDataAgentApprovalTypedData,
  type AsterDataAgentPreparation,
} from './aster-data-agent.ts'
import { buildAsterApproveAgentTypedData } from './aster-approve-agent.ts'
import type { TradingAgentStore } from './storage.ts'
import type { StoredTradingAgent } from './types'

const zeroAddress = '0x0000000000000000000000000000000000000000' as const
const orderChainId = 1666
const ownerChainId = 56
const agentName = 'OrbitalMarkets'
const agentLifetime = 7 * 24 * 60 * 60 * 1000
export const asterBuilderAddress = builderConfig.address.toLowerCase() as Address
export const asterBuilderFeeRate = String(builderConfig.fee / 100_000)

export interface AsterApproveAgentRequest {
  user: Address
  nonce: number
  signature: Hex
  agentName: typeof agentName
  agentAddress: Address
  ipWhitelist: ''
  expired: number
  canSpotTrade: false
  canPerpTrade: true
  canWithdraw: false
  asterChain: 'Mainnet'
  signatureChainId: typeof ownerChainId
  builder: Address
  maxFeeRate: string
  builderName: typeof agentName
  builderNonce: number
  builderSignature: Hex
}

type AsterApproveAgentAction = Omit<AsterApproveAgentRequest, 'signature' | 'builderSignature'>

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
    ipWhitelist: '',
    expired: now + agentLifetime,
    canSpotTrade: false,
    canPerpTrade: true,
    canWithdraw: false,
    asterChain: 'Mainnet',
    signatureChainId: ownerChainId,
    builder: asterBuilderAddress,
    maxFeeRate: asterBuilderFeeRate,
    builderName: agentName,
    builderNonce: now * 1000 + 1,
  }
}

export { buildAsterApproveAgentTypedData }

export function buildAsterApproveBuilderTypedData(action: AsterApproveAgentAction) {
  return {
    domain: {
      name: 'AsterSignTransaction',
      version: '1',
      chainId: BigInt(ownerChainId),
      verifyingContract: zeroAddress,
    },
    types: {
      EIP712Domain: [
        { name: 'name', type: 'string' },
        { name: 'version', type: 'string' },
        { name: 'chainId', type: 'uint256' },
        { name: 'verifyingContract', type: 'address' },
      ] as const,
      ApproveBuilder: [
        { name: 'Builder', type: 'string' },
        { name: 'MaxFeeRate', type: 'string' },
        { name: 'BuilderName', type: 'string' },
        { name: 'AsterChain', type: 'string' },
        { name: 'User', type: 'string' },
        { name: 'Nonce', type: 'uint256' },
      ] as const,
    },
    primaryType: 'ApproveBuilder' as const,
    message: {
      Builder: action.builder,
      MaxFeeRate: action.maxFeeRate,
      BuilderName: action.builderName,
      AsterChain: action.asterChain,
      User: action.user,
      Nonce: BigInt(action.builderNonce),
    },
  }
}

export type AsterApprovalTypedData =
  | ReturnType<typeof buildAsterApproveAgentTypedData>
  | ReturnType<typeof buildAsterApproveBuilderTypedData>
  | AsterDataAgentApprovalTypedData

export async function authorizeAsterAgent(options: {
  storage: TradingAgentStore
  ownerAddress: string
  signTypedData: (typedData: AsterApprovalTypedData) => Promise<Hex>
  relay: (request: AsterApproveAgentRequest) => Promise<'accepted' | 'uncertain'>
  reconcile: (candidateAgentAddresses: string[]) => Promise<string>
  prepareReadOnly: (executionAgent: Address) => Promise<AsterDataAgentPreparation>
  authorizeReadOnly: (
    preparation: AsterDataAgentPreparation,
    signature: Hex,
    executionAgent: Address,
  ) => Promise<void>
  onSignatureStep?: (step: 1 | 2 | 3) => void
  ownerStillCurrent?: () => boolean
  now?: () => number
}): Promise<StoredTradingAgent> {
  const unresolved = await reconcilePendingAsterAgent(options)
  if (unresolved) return unresolved
  const generated = generateAsterAgent()
  const action = buildAsterApproveAgentAction(
    options.ownerAddress,
    generated.agentAddress,
    options.now?.() ?? Date.now(),
  )
  const readOnly = await options.prepareReadOnly(generated.agentAddress)

  options.onSignatureStep?.(1)
  const builderTypedData = buildAsterApproveBuilderTypedData(action)
  const builderSignature = await options.signTypedData(builderTypedData)
  await assertAsterOwnerSignature(builderTypedData, builderSignature, action.user)

  options.onSignatureStep?.(2)
  const readOnlyTypedData = buildAsterDataAgentApprovalTypedData(readOnly.approval)
  const readOnlySignature = await options.signTypedData(readOnlyTypedData)
  await assertAsterOwnerSignature(readOnlyTypedData, readOnlySignature, action.user)

  options.onSignatureStep?.(3)
  const agentTypedData = buildAsterApproveAgentTypedData(action)
  const signature = await options.signTypedData(agentTypedData)
  await assertAsterOwnerSignature(agentTypedData, signature, action.user)
  if (options.ownerStillCurrent && !options.ownerStillCurrent()) {
    throw new Error('Aster owner changed during agent authorization')
  }
  const agent: StoredTradingAgent = {
    version: 2,
    venue: 'aster',
    ownerAddress: options.ownerAddress,
    agentAddress: generated.agentAddress,
    privateKey: generated.privateKey,
    authorizedAt: new Date(Math.floor(action.nonce / 1000)).toISOString(),
    expiresAt: new Date(action.expired).toISOString(),
    builderAddress: asterBuilderAddress,
  }
  await options.storage.savePending(agent)
  let outcome: 'accepted' | 'uncertain'
  try {
    await options.authorizeReadOnly(readOnly, readOnlySignature, generated.agentAddress)
    outcome = await options.relay({ ...action, signature, builderSignature })
  } catch (error) {
    await options.storage.clearPending('aster', options.ownerAddress, agent.agentAddress)
    throw error
  }
  if (outcome === 'accepted') {
    await options.storage.promotePending(agent)
    return agent
  }

  return (await reconcilePendingAsterAgent(options))!
}

export async function reconcilePendingAsterAgent(options: {
  storage: TradingAgentStore
  ownerAddress: string
  reconcile: (candidateAgentAddresses: string[]) => Promise<string>
}): Promise<StoredTradingAgent | null> {
  const pending = await options.storage.loadPendingForSigning('aster', options.ownerAddress)
  if (!pending) return null
  const previous = await options.storage.loadForSigning('aster', options.ownerAddress)
  const reconciledAddress = await options.reconcile([
    pending.agentAddress,
    ...(previous ? [previous.agentAddress] : []),
  ])
  if (reconciledAddress.toLowerCase() === pending.agentAddress.toLowerCase()) {
    await options.storage.promotePending(pending)
    return pending
  }
  if (previous && reconciledAddress.toLowerCase() === previous.agentAddress.toLowerCase()) {
    await options.storage.clearPending('aster', options.ownerAddress, pending.agentAddress)
    return previous
  }
  throw new Error('Aster agent authorization is still being verified')
}

async function assertAsterOwnerSignature(
  typedData: AsterApprovalTypedData,
  signature: Hex,
  ownerAddress: Address,
): Promise<void> {
  const recoveredOwner = typedData.primaryType === 'ApproveAgent'
    ? await recoverTypedDataAddress({ ...typedData, signature })
    : await recoverTypedDataAddress({ ...typedData, signature })
  if (recoveredOwner.toLowerCase() !== ownerAddress.toLowerCase()) {
    throw new Error('Aster approval signature does not match the owner wallet')
  }
}

export async function signAsterAgentRequest(
  request: SigningRequest,
  agent: StoredTradingAgent,
): Promise<SignedAction> {
  const payload = allowedAsterRequest(request, agent)
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

function allowedAsterRequest(request: SigningRequest, agent: StoredTradingAgent): AsterOrderPayload {
  const payload = request.unsigned_payload as Record<string, unknown> | null
  const domain = payload?.domain as Record<string, unknown> | undefined
  const types = payload?.types as Record<string, unknown> | undefined
  const message = payload?.message as Record<string, unknown> | undefined
  const msg = message?.msg
  const orderAction = request.action === 'open' || request.action === 'close' ||
    request.action === 'unwind' || request.action === 'emergency_close'
  const leverageAction = request.action === 'update_leverage'
  const allowed =
    request.venue === 'aster' && agent.venue === 'aster' &&
    request.account.toLowerCase() === agent.ownerAddress.toLowerCase() &&
    request.signer?.toLowerCase() === agent.agentAddress.toLowerCase() &&
    !!agent.expiresAt && Date.parse(agent.expiresAt) > Date.now() &&
    Date.parse(request.expires_at) > Date.now() && (orderAction || leverageAction) &&
    hasOnlyKeys(payload, ['domain', 'types', 'primaryType', 'message']) &&
    payload?.primaryType === 'Message' &&
    hasOnlyKeys(domain, ['name', 'version', 'chainId', 'verifyingContract']) &&
    domain?.name === 'AsterSignTransaction' && domain.version === '1' &&
    domain.chainId === orderChainId && domain.verifyingContract === zeroAddress &&
    hasOnlyKeys(types, ['EIP712Domain', 'Message']) &&
    isAsterDomainType(types?.EIP712Domain) && isAsterMessageType(types?.Message) &&
    hasOnlyKeys(message, ['msg']) && typeof msg === 'string'
  if (!allowed) throw new Error(orderAction ? 'Aster payload is not an allowed IOC order' : 'Aster payload is not an allowed private request')

  const query = parseUniqueQuery(msg)
  if (leverageAction) {
    validateAsterLeverageQuery(request, agent, query)
    return {
      domain: {
        name: 'AsterSignTransaction', version: '1', chainId: orderChainId,
        verifyingContract: zeroAddress,
      },
      message: { msg },
    }
  }
  if (request.action === 'open' ? request.reduce_only : !request.reduce_only) {
    throw new Error('Aster payload is not an allowed IOC order')
  }
  const chargesBuilderFee = request.action === 'open' || request.action === 'close'
  const expectedKeys = [
    'symbol', 'type', 'side', 'quantity', 'price', 'timeInForce',
    'newClientOrderId', 'newOrderRespType', 'reduceOnly', 'positionSide',
    'asterChain', 'user', 'signer', 'nonce',
  ]
  if (chargesBuilderFee) expectedKeys.push('builder', 'feeRate')
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
    (!chargesBuilderFee || (
      agent.builderAddress?.toLowerCase() === asterBuilderAddress &&
      query.get('builder')?.toLowerCase() === asterBuilderAddress &&
      query.get('feeRate') === asterBuilderFeeRate
    )) &&
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

function validateAsterLeverageQuery(
  request: SigningRequest,
  agent: StoredTradingAgent,
  query: Map<string, string>,
): void {
  const metadata = request.venue_metadata as Record<string, unknown> | undefined
  const expectedKeys = ['symbol', 'leverage', 'asterChain', 'user', 'signer', 'nonce']
  const summaryEmpty = request.side === '' && request.amount === 0 && request.price === 0 && !request.reduce_only
  const valid =
    summaryEmpty && request.action === 'update_leverage' && !!request.symbol && request.client_order_id === '' &&
    hasOnlyKeys(metadata, ['method', 'path']) && metadata?.method === 'POST' && metadata.path === '/fapi/v3/leverage' &&
    query.size === expectedKeys.length && expectedKeys.every((key) => query.has(key)) &&
    query.get('asterChain') === 'Mainnet' &&
    query.get('user')?.toLowerCase() === request.account.toLowerCase() &&
    query.get('signer')?.toLowerCase() === agent.agentAddress.toLowerCase() &&
    validAsterNonce(query.get('nonce'), request) &&
    query.get('symbol') === request.symbol &&
    Number.isSafeInteger(request.leverage) && request.leverage! >= 1 && request.leverage! <= 125 &&
    query.get('leverage') === String(request.leverage)
  if (!valid) throw new Error('Aster payload is not an allowed private request')
}

function validAsterNonce(value: string | undefined, request: SigningRequest): boolean {
  if (!value || !/^\d+$/.test(value)) return false
  const nonce = Number(value)
  const createdAt = Date.parse(request.created_at)
  const expiresAt = Date.parse(request.expires_at)
  return Number.isSafeInteger(nonce) && Number.isFinite(createdAt) && Number.isFinite(expiresAt) &&
    nonce >= createdAt * 1_000 && nonce < (expiresAt + 1) * 1_000
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
