import assert from 'node:assert/strict'
import test from 'node:test'
import bs58 from 'bs58'
import nacl from 'tweetnacl'

import {
  authorizePacificaAgent,
  buildPacificaSigningMessage,
  signPacificaAgentRequest,
} from '../src/agents/pacifica-agent.ts'
import type { StorageLike } from '../src/agents/storage.ts'
import type { StoredTradingAgent } from '../src/agents/types.ts'
import type { SigningRequest } from '../src/types/signing.ts'

const ownerAddress = 'FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF'
const agentAddress = '3ogUn1GNXoASaRbxPNeVJnVv5rG4EPBtmQmX61jVorUe'

test('Pacifica bind_agent_wallet canonical bytes match the official fixture', () => {
  const message = buildPacificaSigningMessage('bind_agent_wallet', 1_748_970_123_456, 5_000, {
    agent_wallet: agentAddress,
  })

  assert.equal(
    new TextDecoder().decode(message),
    '{"data":{"agent_wallet":"3ogUn1GNXoASaRbxPNeVJnVv5rG4EPBtmQmX61jVorUe"},"expiry_window":5000,"timestamp":1748970123456,"type":"bind_agent_wallet"}',
  )
  const owner = nacl.sign.keyPair.fromSeed(Uint8Array.from({ length: 32 }, (_, index) => index))
  assert.equal(
    bs58.encode(nacl.sign.detached(message, owner.secretKey)),
    '4N6Mvb9kwUiupsuXmLpvcDNDQzTFyoATDRFHquzPTLFwsYh5Mf3ikBfFHwX8cavJizc5KuqSPQCV6daGJgWfnxGe',
  )
})

test('a local Pacifica agent reproduces the official market-order signature', async () => {
  const signed = await signPacificaAgentRequest(pacificaSigningRequest(), pacificaAgent())

  assert.equal(
    signed.signature,
    '4ocxSPtuRPfQb734p6geJd5NELvXdPPMuT29QuWvAuZQf2soUaSe3T6XC5Js5q9XAmJcN3MNQamkJSZu6ekA93B9',
  )
  assert.equal(signed.signer_address, agentAddress)
  assert.equal(JSON.stringify(signed).includes(pacificaAgent().privateKey), false)
})

test('Pacifica authorization relays no private key and persists only after binding', async () => {
  const storage = new TestStorage()
  let relayed = ''
  const agent = await authorizePacificaAgent({
    storage,
    ownerAddress,
    now: () => 1_748_970_123_456,
    signMessage: async () => new Uint8Array(64),
    relay: async (request) => {
      relayed = JSON.stringify(request)
      assert.equal(storage.values.size, 0)
    },
  })

  assert.equal(relayed.includes(agent.privateKey), false)
  assert.equal(relayed.includes('private'), false)
  assert.equal(relayed.includes('bind_agent_wallet'), false)
})

test('a local Pacifica agent rejects non-order payloads', async () => {
  const request = pacificaSigningRequest()
  request.unsigned_payload = { timestamp: 1_748_970_123_456, expiry_window: 5_000 }
  await assert.rejects(signPacificaAgentRequest(request, pacificaAgent()), /not an allowed market order/)
})

function pacificaAgent(): StoredTradingAgent {
  const keyPair = nacl.sign.keyPair.fromSeed(Uint8Array.from({ length: 32 }, (_, index) => index + 32))
  return {
    version: 1,
    venue: 'pacifica',
    ownerAddress,
    agentAddress: bs58.encode(keyPair.publicKey),
    privateKey: bs58.encode(keyPair.secretKey),
    authorizedAt: '2026-08-10T12:00:00.000Z',
  }
}

function pacificaSigningRequest(): SigningRequest {
  return {
    id: 'request-1',
    client_order_id: '12345678-1234-1234-1234-123456789abc',
    venue: 'pacifica',
    action: 'open',
    account: ownerAddress,
    signer: agentAddress,
    symbol: 'BTC',
    side: 'buy',
    amount: 0.1,
    price: 100,
    reduce_only: false,
    unsigned_payload: {
      timestamp: 1_748_970_123_456,
      expiry_window: 5_000,
      symbol: 'BTC',
      side: 'bid',
      amount: '0.1',
      reduce_only: false,
      slippage_percent: '0.5',
      client_order_id: '12345678-1234-1234-1234-123456789abc',
    },
    expires_at: '2099-08-10T12:00:00.000Z',
    created_at: '2026-08-10T12:00:00.000Z',
  }
}

class TestStorage implements StorageLike {
  readonly values = new Map<string, string>()
  getItem(key: string) { return this.values.get(key) ?? null }
  setItem(key: string, value: string) { this.values.set(key, value) }
  removeItem(key: string) { this.values.delete(key) }
}
