import assert from 'node:assert/strict'
import test from 'node:test'
import bs58 from 'bs58'
import nacl from 'tweetnacl'

import {
  authorizePacificaAgent,
  buildPacificaSigningMessage,
  pacificaBuilderCode,
  pacificaBuilderMaxFeeRate,
  revokePacificaAgent,
  signPacificaAgentRequest,
} from '../src/agents/pacifica-agent.ts'
import { signWithStoredTradingAgent } from '../src/agents/signing.ts'
import type { StoredTradingAgent } from '../src/agents/types.ts'
import type { SigningRequest } from '../src/types/signing.ts'
import { TestTradingAgentStore } from './trading-agent-test-store.ts'

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

test('a local Pacifica agent signs the configured builder code', async () => {
  const storage = new TestTradingAgentStore()
  await storage.save(pacificaAgent())
  const signed = await signWithStoredTradingAgent(storage, pacificaSigningRequest())

  const request = pacificaSigningRequest()
  const order = request.unsigned_payload as Record<string, unknown>
  const expectedMessage = buildPacificaSigningMessage('create_market_order', order.timestamp as number, order.expiry_window as number, {
    amount: order.amount,
    builder_code: pacificaBuilderCode,
    client_order_id: order.client_order_id,
    reduce_only: order.reduce_only,
    side: order.side,
    slippage_percent: order.slippage_percent,
    symbol: order.symbol,
  })
  const keyPair = nacl.sign.keyPair.fromSecretKey(bs58.decode(pacificaAgent().privateKey))
  assert.equal(signed.signature, bs58.encode(nacl.sign.detached(expectedMessage, keyPair.secretKey)))
  assert.equal(signed.signer_address, agentAddress)
  assert.equal(JSON.stringify(signed).includes(pacificaAgent().privateKey), false)
})

test('the signing dispatcher rejects an unsupported venue before loading an agent', async () => {
  const request = { ...pacificaSigningRequest(), venue: 'dydx' } as unknown as SigningRequest
  await assert.rejects(
    signWithStoredTradingAgent(new TestTradingAgentStore(), request),
    /Unsupported signing venue: dydx/,
  )
})

test('a local Pacifica agent signs only the prepared leverage update', async () => {
  const request = pacificaSigningRequest()
  request.id = 'leverage-1'
  request.client_order_id = 'leverage-1'
  request.action = 'update_leverage'
  request.leverage = 2
  request.unsigned_payload = {
    timestamp: 1_748_970_123_456,
    expiry_window: 30_000,
    symbol: 'BTC',
    leverage: 2,
  }
  const signed = await signPacificaAgentRequest(request, pacificaAgent())
  const expectedMessage = buildPacificaSigningMessage('update_leverage', 1_748_970_123_456, 30_000, {
    leverage: 2,
    symbol: 'BTC',
  })
  const keyPair = nacl.sign.keyPair.fromSecretKey(bs58.decode(pacificaAgent().privateKey))
  assert.equal(signed.signature, bs58.encode(nacl.sign.detached(expectedMessage, keyPair.secretKey)))

  request.leverage = 3
  await assert.rejects(signPacificaAgentRequest(request, pacificaAgent()), /not an allowed leverage update/)
})

test('Pacifica authorization relays no private key and persists only after binding', async () => {
  const storage = new TestTradingAgentStore()
  let relayed = ''
  let builderRelayed = ''
  let now = 1_748_970_123_456
  const agent = await authorizePacificaAgent({
    storage,
    ownerAddress,
    builderCodeApproved: async () => false,
    now: () => now++,
    signMessage: async () => new Uint8Array(64),
    relay: async (request) => {
      relayed = JSON.stringify(request)
      assert.equal(storage.values.size, 0)
    },
    relayRevocation: async () => undefined,
    rememberPendingRevocation: () => undefined,
    forgetPendingRevocation: () => undefined,
    relayBuilderApproval: async (request) => {
      builderRelayed = JSON.stringify(request)
      assert.equal(storage.values.size, 0)
    },
  })

  assert.equal(relayed.includes(agent.privateKey), false)
  assert.equal(relayed.includes('private'), false)
  assert.equal(relayed.includes('bind_agent_wallet'), false)
  assert.equal(JSON.parse(relayed).expiry_window, 30_000)
  assert.equal(JSON.parse(relayed).timestamp, 1_748_970_123_457)
  assert.deepEqual(JSON.parse(builderRelayed), {
    account: ownerAddress,
    agent_wallet: null,
    signature: bs58.encode(new Uint8Array(64)),
    timestamp: 1_748_970_123_456,
    expiry_window: 30_000,
    builder_code: pacificaBuilderCode,
    max_fee_rate: pacificaBuilderMaxFeeRate,
  })
})

test('Pacifica authorization skips an already-approved builder code', async () => {
  const storage = new TestTradingAgentStore()
  let signatures = 0
  await authorizePacificaAgent({
    storage,
    ownerAddress,
    builderCodeApproved: async () => true,
    signMessage: async () => {
      signatures++
      return new Uint8Array(64)
    },
    relayBuilderApproval: async () => {
      throw new Error('builder approval should be skipped')
    },
    relay: async () => undefined,
    relayRevocation: async () => undefined,
    rememberPendingRevocation: () => undefined,
    forgetPendingRevocation: () => undefined,
  })

  assert.equal(signatures, 1)
})

test('Pacifica compensates with revoke when encrypted storage fails after binding', async () => {
  const storage = new TestTradingAgentStore()
  storage.save = async () => { throw new Error('IndexedDB unavailable') }
  let revokedAgent = ''
  let signatures = 0

  await assert.rejects(
    authorizePacificaAgent({
      storage,
      ownerAddress,
      builderCodeApproved: async () => true,
      signMessage: async () => {
        signatures++
        return new Uint8Array(64)
      },
      relayBuilderApproval: async () => undefined,
      relay: async () => undefined,
      relayRevocation: async (request) => { revokedAgent = request.agent_wallet },
      rememberPendingRevocation: () => undefined,
      forgetPendingRevocation: () => undefined,
    }),
    /IndexedDB unavailable/,
  )

  assert.equal(signatures, 2)
  assert.notEqual(revokedAgent, '')
})

test('Pacifica retains a public cleanup handle when compensation fails', async () => {
  const storage = new TestTradingAgentStore()
  storage.save = async () => { throw new Error('IndexedDB unavailable') }
  let pendingAgent = ''

  await assert.rejects(
    authorizePacificaAgent({
      storage,
      ownerAddress,
      builderCodeApproved: async () => true,
      signMessage: async () => new Uint8Array(64),
      relayBuilderApproval: async () => undefined,
      relay: async () => undefined,
      relayRevocation: async () => { throw new Error('User rejected') },
      rememberPendingRevocation: (agentAddress) => { pendingAgent = agentAddress },
      forgetPendingRevocation: () => undefined,
    }),
    /could not be stored or revoked/,
  )

  assert.notEqual(pendingAgent, '')
})

test('Pacifica revocation signs and relays the official revoke_agent_wallet payload', async () => {
  let signedMessage = ''
  let relayed = ''
  await revokePacificaAgent({
    ownerAddress,
    agentAddress,
    now: () => 1_748_970_123_456,
    signMessage: async (message) => {
      signedMessage = new TextDecoder().decode(message)
      return new Uint8Array(64)
    },
    relay: async (request) => { relayed = JSON.stringify(request) },
  })

  assert.equal(
    signedMessage,
    '{"data":{"agent_wallet":"3ogUn1GNXoASaRbxPNeVJnVv5rG4EPBtmQmX61jVorUe"},"expiry_window":30000,"timestamp":1748970123456,"type":"revoke_agent_wallet"}',
  )
  assert.deepEqual(JSON.parse(relayed), {
    account: ownerAddress,
    signature: bs58.encode(new Uint8Array(64)),
    timestamp: 1_748_970_123_456,
    expiry_window: 30_000,
    agent_wallet: agentAddress,
  })
})

test('Pacifica revocation rejects an invalid wallet signature before relay', async () => {
  let relayed = false
  await assert.rejects(
    revokePacificaAgent({
      ownerAddress,
      agentAddress,
      signMessage: async () => new Uint8Array(63),
      relay: async () => { relayed = true },
    }),
    /Invalid Pacifica revocation signature/,
  )
  assert.equal(relayed, false)
})

test('a local Pacifica agent rejects non-order payloads', async () => {
  const request = pacificaSigningRequest()
  request.unsigned_payload = { timestamp: 1_748_970_123_456, expiry_window: 5_000 }
  await assert.rejects(signPacificaAgentRequest(request, pacificaAgent()), /not an allowed market order/)
})

test('a local Pacifica agent rejects an altered builder code', async () => {
  const request = pacificaSigningRequest()
  request.unsigned_payload = { ...(request.unsigned_payload as object), builder_code: 'otherbuilder' }
  await assert.rejects(signPacificaAgentRequest(request, pacificaAgent()), /not an allowed market order/)
})

test('a local Pacifica agent rejects a payload side that differs from the request', async () => {
  const request = pacificaSigningRequest()
  request.unsigned_payload = { ...(request.unsigned_payload as object), side: 'ask' }
  await assert.rejects(signPacificaAgentRequest(request, pacificaAgent()), /not an allowed market order/)
})

test('a local Pacifica agent signs fee-free recovery but rejects fee-free normal close', async () => {
  const request = pacificaSigningRequest()
  const order = request.unsigned_payload as Record<string, unknown>
  delete order.builder_code
  order.reduce_only = true
  request.action = 'emergency_close'
  request.reduce_only = true

  const agent = pacificaAgent()
  delete agent.builderCode
  const signed = await signPacificaAgentRequest(request, agent)
  assert.equal(signed.signer_address, agentAddress)

  request.action = 'close'
  await assert.rejects(signPacificaAgentRequest(request, agent), /not an allowed market order/)
})

function pacificaAgent(): StoredTradingAgent {
  const keyPair = nacl.sign.keyPair.fromSeed(Uint8Array.from({ length: 32 }, (_, index) => index + 32))
  return {
    version: 2,
    venue: 'pacifica',
    ownerAddress,
    agentAddress: bs58.encode(keyPair.publicKey),
    privateKey: bs58.encode(keyPair.secretKey),
    authorizedAt: '2026-08-10T12:00:00.000Z',
    builderCode: pacificaBuilderCode,
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
      builder_code: pacificaBuilderCode,
    },
    expires_at: '2099-08-10T12:00:00.000Z',
    created_at: '2026-08-10T12:00:00.000Z',
  }
}
