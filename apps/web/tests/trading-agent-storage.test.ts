import assert from 'node:assert/strict'
import test from 'node:test'
import bs58 from 'bs58'
import nacl from 'tweetnacl'

import {
  clearStoredTradingAgent,
  loadStoredTradingAgent,
  saveStoredTradingAgent,
  storageKey,
  type StorageLike,
} from '../src/agents/storage.ts'
import type { StoredTradingAgent } from '../src/agents/types.ts'

class MemoryStorage implements StorageLike {
  readonly values = new Map<string, string>()

  getItem(key: string) {
    return this.values.get(key) ?? null
  }

  setItem(key: string, value: string) {
    this.values.set(key, value)
  }

  removeItem(key: string) {
    this.values.delete(key)
  }
}

const hyperliquidAgent: StoredTradingAgent = {
  version: 1,
  venue: 'hyperliquid',
  ownerAddress: '0xAABBccDDeeFF0011223344556677889900AaBbCc',
  agentAddress: '0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A',
  privateKey: '0x1111111111111111111111111111111111111111111111111111111111111111',
  authorizedAt: '2026-08-10T12:00:00.000Z',
}

test('stored trading agents round trip under a normalized owner key', () => {
  const storage = new MemoryStorage()
  saveStoredTradingAgent(storage, hyperliquidAgent)

  assert.equal(
    storage.values.has('orbital.agent.hyperliquid.v1:0xaabbccddeeff0011223344556677889900aabbcc'),
    true,
  )
  assert.deepEqual(
    loadStoredTradingAgent(storage, 'hyperliquid', hyperliquidAgent.ownerAddress.toUpperCase()),
    { ...hyperliquidAgent, ownerAddress: hyperliquidAgent.ownerAddress.toLowerCase() },
  )
})

test('malformed and key-mismatched records are deleted', () => {
  const storage = new MemoryStorage()
  const key = storageKey('pacifica', 'Owner111')
  storage.setItem(key, '{not-json')

  assert.equal(loadStoredTradingAgent(storage, 'pacifica', 'Owner111'), null)
  assert.equal(storage.getItem(key), null)

  const agent: StoredTradingAgent = {
    version: 1,
    venue: 'pacifica',
    ownerAddress: 'Owner111',
    agentAddress: 'Agent111',
    privateKey: 'secret',
    authorizedAt: '2026-08-10T12:00:00.000Z',
  }
  saveStoredTradingAgent(storage, agent)

  assert.equal(loadStoredTradingAgent(storage, 'pacifica', 'Owner111'), null)
  assert.equal(storage.getItem(key), null)
})

test('an agent cannot be loaded for another owner', () => {
  const storage = new MemoryStorage()
  saveStoredTradingAgent(storage, hyperliquidAgent)

  assert.equal(
    loadStoredTradingAgent(storage, 'hyperliquid', '0x0000000000000000000000000000000000000001'),
    null,
  )
})

test('Pacifica key material must derive the stored public key', () => {
  const storage = new MemoryStorage()
  const keyPair = nacl.sign.keyPair.fromSeed(Uint8Array.from({ length: 32 }, (_, index) => index + 32))
  const agent: StoredTradingAgent = {
    version: 1,
    venue: 'pacifica',
    ownerAddress: 'FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF',
    agentAddress: bs58.encode(keyPair.publicKey),
    privateKey: bs58.encode(keyPair.secretKey),
    authorizedAt: '2026-08-10T12:00:00.000Z',
  }
  saveStoredTradingAgent(storage, agent)

  assert.deepEqual(loadStoredTradingAgent(storage, 'pacifica', agent.ownerAddress), agent)
})

test('clear removes only the selected owner and venue agent', () => {
  const storage = new MemoryStorage()
  saveStoredTradingAgent(storage, hyperliquidAgent)

  clearStoredTradingAgent(storage, 'hyperliquid', hyperliquidAgent.ownerAddress)

  assert.equal(storage.values.size, 0)
})
