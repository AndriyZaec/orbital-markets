import bs58 from 'bs58'
import nacl from 'tweetnacl'
import { privateKeyToAccount } from 'viem/accounts'
import type { Hex } from 'viem'

import type { StoredTradingAgent, Venue } from './types'

export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

function normalizeOwner(venue: Venue, ownerAddress: string): string {
  return venue === 'hyperliquid' ? ownerAddress.toLowerCase() : ownerAddress
}

function keyPairMatches(agent: StoredTradingAgent): boolean {
  try {
    if (agent.venue === 'hyperliquid') {
      if (!/^0x[0-9a-fA-F]{64}$/.test(agent.privateKey)) return false
      return privateKeyToAccount(agent.privateKey as Hex).address.toLowerCase() === agent.agentAddress.toLowerCase()
    }

    const secretKey = bs58.decode(agent.privateKey)
    if (secretKey.length !== nacl.sign.secretKeyLength) return false
    const publicKey = nacl.sign.keyPair.fromSecretKey(secretKey).publicKey
    return bs58.encode(publicKey) === agent.agentAddress
  } catch {
    return false
  }
}

export function storageKey(venue: Venue, ownerAddress: string): string {
  return `orbital.agent.${venue}.v1:${normalizeOwner(venue, ownerAddress)}`
}

function isStoredTradingAgent(value: unknown): value is StoredTradingAgent {
  if (!value || typeof value !== 'object') return false
  const agent = value as Partial<StoredTradingAgent>
  return (
    agent.version === 1 &&
    (agent.venue === 'hyperliquid' || agent.venue === 'pacifica') &&
    typeof agent.ownerAddress === 'string' &&
    agent.ownerAddress.length > 0 &&
    typeof agent.agentAddress === 'string' &&
    agent.agentAddress.length > 0 &&
    typeof agent.privateKey === 'string' &&
    agent.privateKey.length > 0 &&
    typeof agent.authorizedAt === 'string' &&
    Number.isFinite(Date.parse(agent.authorizedAt))
  )
}

export function saveStoredTradingAgent(storage: StorageLike, agent: StoredTradingAgent): void {
  const normalized = {
    ...agent,
    ownerAddress: normalizeOwner(agent.venue, agent.ownerAddress),
  }
  storage.setItem(storageKey(agent.venue, agent.ownerAddress), JSON.stringify(normalized))
}

export function loadStoredTradingAgent(
  storage: StorageLike,
  venue: Venue,
  ownerAddress: string,
): StoredTradingAgent | null {
  const key = storageKey(venue, ownerAddress)
  const encoded = storage.getItem(key)
  if (!encoded) return null

  try {
    const agent: unknown = JSON.parse(encoded)
    if (
      !isStoredTradingAgent(agent) ||
      agent.venue !== venue ||
      normalizeOwner(venue, agent.ownerAddress) !== normalizeOwner(venue, ownerAddress) ||
      !keyPairMatches(agent)
    ) {
      storage.removeItem(key)
      return null
    }
    return agent
  } catch {
    storage.removeItem(key)
    return null
  }
}

export function clearStoredTradingAgent(
  storage: StorageLike,
  venue: Venue,
  ownerAddress: string,
): void {
  storage.removeItem(storageKey(venue, ownerAddress))
}
