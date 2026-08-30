import { toHex, type Address, type Hex } from 'viem'
import { keccak256 } from 'viem'
import { generatePrivateKey, privateKeyToAccount } from 'viem/accounts'
import { encode } from '@msgpack/msgpack'
import builderConfig from '../../../api/internal/venue/hyperliquid/live/builder_config.json' with { type: 'json' }

import type { SignedAction, SigningRequest } from '@/types/signing'
import { saveStoredTradingAgent, type StorageLike } from './storage.ts'
import type { StoredTradingAgent } from './types'

const zeroAddress = '0x0000000000000000000000000000000000000000' as const
const agentName = 'Orbital Markets'
const hyperliquidInfoUrl = 'https://api.hyperliquid.xyz/info'
export const hyperliquidBuilderAddress = builderConfig.address as Address
export const hyperliquidBuilderFee = builderConfig.fee

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
  owner_address: Address
  action: HyperliquidApproveAgentAction
  signature: { r: Hex; s: Hex; v: 27 | 28 }
}

export interface HyperliquidApproveBuilderFeeAction {
  [key: string]: unknown
  type: 'approveBuilderFee'
  hyperliquidChain: 'Mainnet'
  signatureChainId: Hex
  maxFeeRate: string
  builder: Address
  nonce: number
}

export interface HyperliquidApproveBuilderFeeRequest {
  owner_address: Address
  action: HyperliquidApproveBuilderFeeAction
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

export function buildHyperliquidApproveBuilderFeeAction(
  builderAddress: string,
  chainId: number,
  nonce: number,
): HyperliquidApproveBuilderFeeAction {
  if (!/^0x[0-9a-fA-F]{40}$/.test(builderAddress)) throw new Error('Invalid Hyperliquid builder address')
  if (!Number.isSafeInteger(chainId) || chainId <= 0) throw new Error('Invalid wallet chain ID')
  if (!Number.isSafeInteger(nonce) || nonce <= 0) throw new Error('Invalid builder approval nonce')
  return {
    type: 'approveBuilderFee',
    hyperliquidChain: 'Mainnet',
    signatureChainId: toHex(chainId),
    maxFeeRate: builderConfig.maxFeeRate,
    builder: builderAddress.toLowerCase() as Address,
    nonce,
  }
}

export function buildHyperliquidApproveBuilderFeeTypedData(action: HyperliquidApproveBuilderFeeAction) {
  return {
    domain: {
      name: 'HyperliquidSignTransaction',
      version: '1',
      chainId: Number.parseInt(action.signatureChainId, 16),
      verifyingContract: zeroAddress,
    },
    types: {
      'HyperliquidTransaction:ApproveBuilderFee': [
        { name: 'hyperliquidChain', type: 'string' },
        { name: 'maxFeeRate', type: 'string' },
        { name: 'builder', type: 'address' },
        { name: 'nonce', type: 'uint64' },
      ] as const,
    },
    primaryType: 'HyperliquidTransaction:ApproveBuilderFee' as const,
    message: { ...action, nonce: BigInt(action.nonce) },
  }
}

export async function authorizeHyperliquidAgent(options: {
  storage: StorageLike
  ownerAddress: string
  chainId: number
  builderFeeApproved: (ownerAddress: string, builderAddress: string) => Promise<boolean>
  signTypedData: (typedData: ReturnType<typeof buildHyperliquidApproveAgentTypedData>) => Promise<Hex>
  signBuilderTypedData: (typedData: ReturnType<typeof buildHyperliquidApproveBuilderFeeTypedData>) => Promise<Hex>
  relay: (request: HyperliquidApproveAgentRequest) => Promise<void>
  relayBuilderApproval: (request: HyperliquidApproveBuilderFeeRequest) => Promise<void>
  now?: () => number
}): Promise<StoredTradingAgent> {
  if (!/^0x[0-9a-fA-F]{40}$/.test(options.ownerAddress)) throw new Error('Invalid Hyperliquid owner address')
  let previousNonce = 0
  const nextNonce = () => {
    const current = options.now?.() ?? Date.now()
    previousNonce = Math.max(current, previousNonce + 1)
    return previousNonce
  }
  const builderAlreadyApproved = await options.builderFeeApproved(options.ownerAddress, hyperliquidBuilderAddress)
  const approveBuilder = !builderAlreadyApproved
  if (approveBuilder) {
    await approveHyperliquidBuilderFee({
      ownerAddress: options.ownerAddress,
      builderAddress: hyperliquidBuilderAddress,
      chainId: options.chainId,
      now: nextNonce(),
      signTypedData: options.signBuilderTypedData,
      relay: options.relayBuilderApproval,
    })
  }
  const generated = generateHyperliquidAgent()
  const agentNonce = nextNonce()
  const action = buildHyperliquidApproveAgentAction(generated.agentAddress, options.chainId, agentNonce)
  const ownerSignature = await options.signTypedData(buildHyperliquidApproveAgentTypedData(action))
  await options.relay({
    owner_address: options.ownerAddress as Address,
    action,
    signature: splitEthereumSignature(ownerSignature),
  })

  const agent: StoredTradingAgent = {
    version: 1,
    venue: 'hyperliquid',
    ownerAddress: options.ownerAddress,
    agentAddress: generated.agentAddress,
    privateKey: generated.privateKey,
    authorizedAt: new Date(agentNonce).toISOString(),
    builderAddress: hyperliquidBuilderAddress,
  }
  saveStoredTradingAgent(options.storage, agent)
  return agent
}

export async function approveHyperliquidBuilderFee(options: {
  ownerAddress: string
  builderAddress: string
  chainId: number
  signTypedData: (typedData: ReturnType<typeof buildHyperliquidApproveBuilderFeeTypedData>) => Promise<Hex>
  relay: (request: HyperliquidApproveBuilderFeeRequest) => Promise<void>
  now?: number
}): Promise<void> {
  const action = buildHyperliquidApproveBuilderFeeAction(
    options.builderAddress,
    options.chainId,
    options.now ?? Date.now(),
  )
  const signature = await options.signTypedData(buildHyperliquidApproveBuilderFeeTypedData(action))
  await options.relay({
    owner_address: options.ownerAddress as Address,
    action,
    signature: splitEthereumSignature(signature),
  })
}

export async function hasApprovedHyperliquidBuilderFee(
  ownerAddress: string,
  builderAddress: string,
  fetcher: typeof fetch = fetch,
): Promise<boolean> {
  try {
    const response = await fetcher(hyperliquidInfoUrl, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ type: 'maxBuilderFee', user: ownerAddress, builder: builderAddress }),
    })
    if (!response.ok) throw new Error(`Hyperliquid builder allowance request failed with ${response.status}`)
    const approved: unknown = await response.json()
    if (typeof approved !== 'number' || !Number.isFinite(approved)) {
      throw new Error('Hyperliquid returned an invalid builder allowance')
    }
    return approved >= hyperliquidBuilderFee
  } catch (error) {
    if (error instanceof Error && error.message.startsWith('Hyperliquid')) throw error
    throw new Error('Unable to verify Hyperliquid builder allowance. Please try again.', { cause: error })
  }
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
  const payload = request.action === 'update_leverage'
    ? allowedL1LeveragePayload(request, agent)
    : allowedL1OrderPayload(request, agent)
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

interface HyperliquidOrderAction {
  type: 'order'
  orders: [{
    a: number
    b: boolean
    p: string
    s: string
    r: boolean
    t: { limit: { tif: 'Ioc' } }
    c: string
  }]
  grouping: 'na'
  builder?: { b: Address; f: 20 }
}

interface HyperliquidLeverageAction {
  type: 'updateLeverage'
  asset: number
  isCross: true
  leverage: number
}

function allowedL1LeveragePayload(request: SigningRequest, agent: StoredTradingAgent): L1OrderPayload {
  const payload = request.unsigned_payload as Record<string, unknown> | null
  const domain = payload?.domain as Record<string, unknown> | undefined
  const message = payload?.message as Record<string, unknown> | undefined
  const action = payload?.action as Record<string, unknown> | undefined
  const types = payload?.types as Record<string, unknown> | undefined
  const connectionId = message?.connectionId
  const valid =
    request.venue === 'hyperliquid' &&
    agent.venue === 'hyperliquid' &&
    request.account.toLowerCase() === agent.ownerAddress.toLowerCase() &&
    request.signer?.toLowerCase() === agent.agentAddress.toLowerCase() &&
    request.action === 'update_leverage' &&
    Date.parse(request.expires_at) > Date.now() &&
    payload?.primaryType === 'Agent' &&
    domain?.chainId === 1337 && domain?.name === 'Exchange' && domain?.version === '1' &&
    domain?.verifyingContract === zeroAddress && isAgentType(types?.Agent) &&
    message?.source === 'a' && typeof connectionId === 'string' && /^0x[0-9a-fA-F]{64}$/.test(connectionId) &&
    action?.type === 'updateLeverage' && hasOnlyKeys(action, ['type', 'asset', 'isCross', 'leverage']) &&
    action.asset === request.venue_asset_id && action.isCross === true &&
    Number.isSafeInteger(action.leverage) && action.leverage === request.leverage &&
    Number.isSafeInteger(payload?.nonce) && (payload?.nonce as number) > 0
  if (!valid) throw new Error('Hyperliquid payload is not an allowed leverage update')
  const validated: HyperliquidLeverageAction = {
    type: 'updateLeverage', asset: action.asset as number, isCross: true, leverage: action.leverage as number,
  }
  if ((connectionId as string).toLowerCase() !== l1ConnectionId(validated, payload?.nonce as number).toLowerCase()) {
    throw new Error('Hyperliquid connection ID does not match the leverage update')
  }
  return {
    domain: { chainId: 1337, name: 'Exchange', verifyingContract: zeroAddress, version: '1' },
    message: { source: 'a', connectionId: connectionId as Hex },
  }
}

function allowedL1OrderPayload(request: SigningRequest, agent: StoredTradingAgent): L1OrderPayload {
  const payload = request.unsigned_payload as Record<string, unknown> | null
  const domain = payload?.domain as Record<string, unknown> | undefined
  const message = payload?.message as Record<string, unknown> | undefined
  const action = payload?.action as Record<string, unknown> | undefined
  const types = payload?.types as Record<string, unknown> | undefined
  const agentType = types?.Agent
  const connectionId = message?.connectionId
  const allowed =
    request.venue === 'hyperliquid' &&
    agent.venue === 'hyperliquid' &&
    request.account.toLowerCase() === agent.ownerAddress.toLowerCase() &&
    request.signer?.toLowerCase() === agent.agentAddress.toLowerCase() &&
    (request.action === 'open' || request.action === 'close') &&
    (request.action !== 'close' || request.reduce_only) &&
    Date.parse(request.expires_at) > Date.now() &&
    payload?.primaryType === 'Agent' &&
    domain?.chainId === 1337 &&
    domain?.name === 'Exchange' &&
    domain?.version === '1' &&
    domain?.verifyingContract === zeroAddress &&
    isAgentType(agentType) &&
    message?.source === 'a' &&
    typeof connectionId === 'string' &&
    /^0x[0-9a-fA-F]{64}$/.test(connectionId)
  if (!allowed) throw new Error('Hyperliquid payload is not an allowed L1 order')

  const validatedAction = validateOrderAction(action, request, agent)
  if (!Number.isSafeInteger(payload?.nonce) || (payload?.nonce as number) <= 0) {
    throw new Error('Hyperliquid payload is not an allowed L1 order')
  }
  const expectedConnectionId = l1ConnectionId(validatedAction, payload.nonce as number)
  if ((connectionId as string).toLowerCase() !== expectedConnectionId.toLowerCase()) {
    throw new Error('Hyperliquid connection ID does not match the requested order')
  }

  return {
    domain: {
      chainId: 1337,
      name: 'Exchange',
      verifyingContract: zeroAddress,
      version: '1',
    },
    message: { source: 'a', connectionId: connectionId as Hex },
  }
}

function isAgentType(value: unknown): boolean {
  return JSON.stringify(value) === JSON.stringify([
    { name: 'source', type: 'string' },
    { name: 'connectionId', type: 'bytes32' },
  ])
}

function validateOrderAction(
  value: Record<string, unknown> | undefined,
  request: SigningRequest,
  agent: StoredTradingAgent,
): HyperliquidOrderAction {
  const orders = value?.orders
  const order = Array.isArray(orders) && orders.length === 1
    ? orders[0] as Record<string, unknown>
    : null
  const orderType = order?.t as Record<string, unknown> | undefined
  const limit = orderType?.limit as Record<string, unknown> | undefined
  const expectedBuy = request.side === 'buy'
  const expectedPrices = new Set(
    Array.from({ length: 7 }, (_, sizeDecimals) => normalizeHyperliquidPrice(
      request.price * (expectedBuy ? 1.005 : 0.995),
      sizeDecimals,
    )).map(Number),
  )
  const amount = Number(order?.s)
  const valid =
    value?.type === 'order' &&
    value.grouping === 'na' &&
    hasOnlyKeys(value, value.builder ? ['type', 'orders', 'grouping', 'builder'] : ['type', 'orders', 'grouping']) &&
    validBuilder(value.builder, agent) &&
    !!order &&
    hasOnlyKeys(order, ['a', 'b', 'p', 's', 'r', 't', 'c']) &&
    Number.isSafeInteger(order.a) &&
    (order.a as number) >= 0 &&
    order.a === request.venue_asset_id &&
    order.b === expectedBuy &&
    typeof order.p === 'string' &&
    expectedPrices.has(Number(order.p)) &&
    Number.isFinite(amount) &&
    Math.abs(amount - request.amount) < 1e-12 &&
    order.r === request.reduce_only &&
    hasOnlyKeys(orderType, ['limit']) &&
    hasOnlyKeys(limit, ['tif']) &&
    limit?.tif === 'Ioc' &&
    typeof order.c === 'string' &&
    /^0x[0-9a-fA-F]{32}$/.test(order.c)
  if (!valid) throw new Error('Hyperliquid payload is not an allowed L1 order')

  const builder = value.builder as Record<string, unknown> | undefined
  return {
    type: 'order',
    orders: [{
      a: order.a as number,
      b: order.b as boolean,
      p: order.p as string,
      s: order.s as string,
      r: order.r as boolean,
      t: { limit: { tif: 'Ioc' } },
      c: order.c as string,
    }],
    grouping: 'na',
    ...(builder ? { builder: { b: builder.b as Address, f: 20 as const } } : {}),
  }
}

function validBuilder(value: unknown, agent: StoredTradingAgent): boolean {
  if (!agent.builderAddress) return value === undefined
  if (!value || typeof value !== 'object') return false
  const builder = value as Record<string, unknown>
  return hasOnlyKeys(builder, ['b', 'f']) && builder.b === hyperliquidBuilderAddress && builder.f === hyperliquidBuilderFee
}

function normalizeHyperliquidPrice(price: number, sizeDecimals: number): string {
  const maxDecimals = 6 - sizeDecimals
  const decimalRounded = Number(price.toFixed(maxDecimals))
  const significantDecimals = 4 - Math.floor(Math.log10(decimalRounded))
  const significantFactor = 10 ** significantDecimals
  const normalized = significantDecimals < maxDecimals
    ? Math.round(decimalRounded * significantFactor) / significantFactor
    : decimalRounded
  return normalized
    .toFixed(Math.max(0, Math.min(maxDecimals, significantDecimals)))
    .replace(/\.?0+$/, '')
}

function hasOnlyKeys(value: Record<string, unknown> | undefined, keys: string[]): boolean {
  if (!value) return false
  const actual = Object.keys(value).sort()
  return actual.length === keys.length && actual.every((key, index) => key === [...keys].sort()[index])
}

function l1ConnectionId(action: HyperliquidOrderAction | HyperliquidLeverageAction, nonce: number): Hex {
  const encodedAction = encode(action)
  const input = new Uint8Array(encodedAction.length + 9)
  input.set(encodedAction)
  new DataView(input.buffer).setBigUint64(encodedAction.length, BigInt(nonce), false)
  input[input.length - 1] = 0
  return keccak256(input)
}
