import bs58 from 'bs58'
import nacl from 'tweetnacl'

import type { SignedAction, SigningRequest } from '@/types/signing'
import { saveStoredTradingAgent, type StorageLike } from './storage.ts'
import type { StoredTradingAgent } from './types'

const bindExpiryWindow = 5_000
const maxOrderExpiryWindow = 120_000

export interface PacificaBindAgentRequest {
  account: string
  signature: string
  timestamp: number
  expiry_window: number
  agent_wallet: string
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
  relay: (request: PacificaBindAgentRequest) => Promise<void>
  now?: () => number
}): Promise<StoredTradingAgent> {
  const timestamp = options.now?.() ?? Date.now()
  const generated = generatePacificaAgent()
  const message = buildPacificaSigningMessage('bind_agent_wallet', timestamp, bindExpiryWindow, {
    agent_wallet: generated.agentAddress,
  })
  const ownerSignature = await options.signMessage(message)
  if (ownerSignature.length !== nacl.sign.signatureLength) throw new Error('Invalid Pacifica owner signature')
  await options.relay({
    account: options.ownerAddress,
    signature: bs58.encode(ownerSignature),
    timestamp,
    expiry_window: bindExpiryWindow,
    agent_wallet: generated.agentAddress,
  })

  const agent: StoredTradingAgent = {
    version: 1,
    venue: 'pacifica',
    ownerAddress: options.ownerAddress,
    agentAddress: generated.agentAddress,
    privateKey: generated.privateKey,
    authorizedAt: new Date(timestamp).toISOString(),
  }
  saveStoredTradingAgent(options.storage, agent)
  return agent
}

export async function signPacificaAgentRequest(
  request: SigningRequest,
  agent: StoredTradingAgent,
): Promise<SignedAction> {
  const order = allowedMarketOrder(request, agent)
  const secretKey = bs58.decode(agent.privateKey)
  const keyPair = nacl.sign.keyPair.fromSecretKey(secretKey)
  if (bs58.encode(keyPair.publicKey) !== agent.agentAddress) {
    throw new Error('Pacifica agent key does not match its address')
  }
  const message = buildPacificaSigningMessage(
    'create_market_order',
    order.timestamp,
    order.expiry_window,
    {
      amount: order.amount,
      client_order_id: order.client_order_id,
      reduce_only: order.reduce_only,
      side: order.side,
      slippage_percent: order.slippage_percent,
      symbol: order.symbol,
    },
  )
  return {
    request_id: request.id,
    client_order_id: request.client_order_id,
    venue: request.venue,
    signer_address: agent.agentAddress,
    signature: bs58.encode(nacl.sign.detached(message, keyPair.secretKey)),
  }
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
    (request.action === 'open' || request.action === 'close') &&
    Date.parse(request.expires_at) > Date.now() &&
    Number.isSafeInteger(order?.timestamp) &&
    Number.isSafeInteger(order?.expiry_window) &&
    (order?.expiry_window ?? 0) > 0 &&
    (order?.expiry_window ?? 0) <= maxOrderExpiryWindow &&
    order?.symbol === request.symbol &&
    (order?.side === 'bid' || order?.side === 'ask') &&
    Number.isFinite(amount) &&
    amount > 0 &&
    Math.abs(amount - request.amount) < 1e-12 &&
    order?.reduce_only === request.reduce_only &&
    (request.action !== 'close' || order.reduce_only === true) &&
    Number.isFinite(slippage) &&
    slippage >= 0 &&
    slippage <= 1 &&
    order?.client_order_id === request.client_order_id
  if (!allowed) throw new Error('Pacifica payload is not an allowed market order')
  return order as PacificaOrder
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
