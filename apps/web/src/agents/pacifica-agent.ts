import bs58 from 'bs58'
import nacl from 'tweetnacl'

import type { SignedAction, SigningRequest } from '@/types/signing'
import builderConfig from '../../../api/internal/venue/pacifica/live/builder_config.json' with { type: 'json' }
import { saveStoredTradingAgent, type StorageLike } from './storage.ts'
import type { StoredTradingAgent } from './types'

const bindExpiryWindow = 30_000
const builderApprovalExpiryWindow = 30_000
const maxOrderExpiryWindow = 120_000

export const pacificaBuilderCode = builderConfig.code
export const pacificaBuilderMaxFeeRate = builderConfig.maxFeeRate

export interface PacificaBindAgentRequest {
  account: string
  signature: string
  timestamp: number
  expiry_window: number
  agent_wallet: string
}

export interface PacificaApproveBuilderCodeRequest {
  account: string
  agent_wallet: null
  signature: string
  timestamp: number
  expiry_window: number
  builder_code: string
  max_fee_rate: string
}

export function generatePacificaAgent(): { privateKey: string; agentAddress: string } {
  const keyPair = nacl.sign.keyPair()
  return {
    privateKey: bs58.encode(keyPair.secretKey),
    agentAddress: bs58.encode(keyPair.publicKey),
  }
}

export function buildPacificaSigningMessage(
  operation: string,
  timestamp: number,
  expiryWindow: number,
  data: Record<string, unknown>,
): Uint8Array {
  const canonical = sortCanonical({
    data,
    expiry_window: expiryWindow,
    timestamp,
    type: operation,
  })
  return new TextEncoder().encode(JSON.stringify(canonical))
}

export async function authorizePacificaAgent(options: {
  storage: StorageLike
  ownerAddress: string
  signMessage: (message: Uint8Array) => Promise<Uint8Array>
  builderCodeApproved: (ownerAddress: string) => Promise<boolean>
  relay: (request: PacificaBindAgentRequest) => Promise<void>
  relayBuilderApproval: (request: PacificaApproveBuilderCodeRequest) => Promise<void>
  now?: () => number
}): Promise<StoredTradingAgent> {
  if (!await options.builderCodeApproved(options.ownerAddress)) {
    const approvalTimestamp = options.now?.() ?? Date.now()
    const approvalMessage = buildPacificaSigningMessage('approve_builder_code', approvalTimestamp, builderApprovalExpiryWindow, {
      builder_code: pacificaBuilderCode,
      max_fee_rate: pacificaBuilderMaxFeeRate,
    })
    const approvalSignature = await options.signMessage(approvalMessage)
    if (approvalSignature.length !== nacl.sign.signatureLength) throw new Error('Invalid Pacifica builder approval signature')
    await options.relayBuilderApproval({
      account: options.ownerAddress,
      agent_wallet: null,
      signature: bs58.encode(approvalSignature),
      timestamp: approvalTimestamp,
      expiry_window: builderApprovalExpiryWindow,
      builder_code: pacificaBuilderCode,
      max_fee_rate: pacificaBuilderMaxFeeRate,
    })
  }
  const generated = generatePacificaAgent()
  const bindTimestamp = options.now?.() ?? Date.now()
  const message = buildPacificaSigningMessage('bind_agent_wallet', bindTimestamp, bindExpiryWindow, {
    agent_wallet: generated.agentAddress,
  })
  const ownerSignature = await options.signMessage(message)
  if (ownerSignature.length !== nacl.sign.signatureLength) throw new Error('Invalid Pacifica owner signature')
  await options.relay({
    account: options.ownerAddress,
    signature: bs58.encode(ownerSignature),
    timestamp: bindTimestamp,
    expiry_window: bindExpiryWindow,
    agent_wallet: generated.agentAddress,
  })

  const agent: StoredTradingAgent = {
    version: 1,
    venue: 'pacifica',
    ownerAddress: options.ownerAddress,
    agentAddress: generated.agentAddress,
    privateKey: generated.privateKey,
    authorizedAt: new Date(bindTimestamp).toISOString(),
    builderCode: pacificaBuilderCode,
  }
  saveStoredTradingAgent(options.storage, agent)
  return agent
}

export async function signPacificaAgentRequest(
  request: SigningRequest,
  agent: StoredTradingAgent,
): Promise<SignedAction> {
  const secretKey = bs58.decode(agent.privateKey)
  const keyPair = nacl.sign.keyPair.fromSecretKey(secretKey)
  if (bs58.encode(keyPair.publicKey) !== agent.agentAddress) {
    throw new Error('Pacifica agent key does not match its address')
  }
  const message = request.action === 'update_leverage'
    ? leverageSigningMessage(allowedLeverageUpdate(request, agent))
    : orderSigningMessage(allowedMarketOrder(request, agent))
  return {
    request_id: request.id,
    client_order_id: request.client_order_id,
    venue: request.venue,
    signer_address: agent.agentAddress,
    signature: bs58.encode(nacl.sign.detached(message, keyPair.secretKey)),
  }
}

function orderSigningMessage(order: PacificaOrder): Uint8Array {
  const data: Record<string, unknown> = {
    amount: order.amount,
    client_order_id: order.client_order_id,
    reduce_only: order.reduce_only,
    side: order.side,
    slippage_percent: order.slippage_percent,
    symbol: order.symbol,
  }
  if (order.builder_code) data.builder_code = order.builder_code
  return buildPacificaSigningMessage('create_market_order', order.timestamp, order.expiry_window, data)
}

interface PacificaLeverageUpdate {
  timestamp: number
  expiry_window: number
  symbol: string
  leverage: number
}

function leverageSigningMessage(update: PacificaLeverageUpdate): Uint8Array {
  return buildPacificaSigningMessage('update_leverage', update.timestamp, update.expiry_window, {
    leverage: update.leverage,
    symbol: update.symbol,
  })
}

function allowedLeverageUpdate(request: SigningRequest, agent: StoredTradingAgent): PacificaLeverageUpdate {
  const update = request.unsigned_payload as Partial<PacificaLeverageUpdate> | null
  const allowed =
    request.venue === 'pacifica' &&
    agent.venue === 'pacifica' &&
    request.account === agent.ownerAddress &&
    request.signer === agent.agentAddress &&
    request.action === 'update_leverage' &&
    Date.parse(request.expires_at) > Date.now() &&
    Number.isSafeInteger(update?.timestamp) &&
    Number.isSafeInteger(update?.expiry_window) &&
    (update?.expiry_window ?? 0) > 0 &&
    (update?.expiry_window ?? 0) <= maxOrderExpiryWindow &&
    update?.symbol === request.symbol &&
    Number.isSafeInteger(update?.leverage) &&
    update?.leverage === request.leverage &&
    hasOnlyKeys(update, ['timestamp', 'expiry_window', 'symbol', 'leverage'])
  if (!allowed) throw new Error('Pacifica payload is not an allowed leverage update')
  return update as PacificaLeverageUpdate
}

interface PacificaOrder {
  timestamp: number
  expiry_window: number
  symbol: string
  side: 'bid' | 'ask'
  amount: string
  reduce_only: boolean
  slippage_percent: string
  client_order_id: string
  builder_code?: string
}

function allowedMarketOrder(request: SigningRequest, agent: StoredTradingAgent): PacificaOrder {
  const order = request.unsigned_payload as Partial<PacificaOrder> | null
  const amount = Number(order?.amount)
  const slippage = Number(order?.slippage_percent)
  const allowed =
    request.venue === 'pacifica' &&
    agent.venue === 'pacifica' &&
    request.account === agent.ownerAddress &&
    request.signer === agent.agentAddress &&
    (request.action === 'open' || request.action === 'close' || request.action === 'unwind' || request.action === 'emergency_close') &&
    Date.parse(request.expires_at) > Date.now() &&
    Number.isSafeInteger(order?.timestamp) &&
    Number.isSafeInteger(order?.expiry_window) &&
    (order?.expiry_window ?? 0) > 0 &&
    (order?.expiry_window ?? 0) <= maxOrderExpiryWindow &&
    order?.symbol === request.symbol &&
    ((request.side === 'buy' && order?.side === 'bid') ||
      (request.side === 'sell' && order?.side === 'ask')) &&
    Number.isFinite(amount) &&
    amount > 0 &&
    Math.abs(amount - request.amount) < 1e-12 &&
    order?.reduce_only === request.reduce_only &&
    (request.action === 'open' || order.reduce_only === true) &&
    Number.isFinite(slippage) &&
    slippage >= 0 &&
    slippage <= 1 &&
    order?.client_order_id === request.client_order_id &&
    validPacificaBuilder(order?.builder_code, request.action, agent)
  if (!allowed) throw new Error('Pacifica payload is not an allowed market order')
  return order as PacificaOrder
}

function validPacificaBuilder(value: unknown, action: string, agent: StoredTradingAgent): boolean {
  if (action === 'unwind' || action === 'emergency_close') return value === undefined
  return agent.builderCode === pacificaBuilderCode && value === pacificaBuilderCode
}

function sortCanonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortCanonical)
  if (!value || typeof value !== 'object') return value
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, entry]) => [key, sortCanonical(entry)]),
  )
}

function hasOnlyKeys(value: object | null | undefined, keys: string[]): boolean {
  if (!value) return false
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  return actual.length === expected.length && actual.every((key, index) => key === expected[index])
}
