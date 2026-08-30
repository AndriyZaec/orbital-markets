import assert from 'node:assert/strict'
import test from 'node:test'
import { privateKeyToAccount } from 'viem/accounts'
import { encode } from '@msgpack/msgpack'
import { keccak256 } from 'viem'

import {
  buildHyperliquidApproveAgentAction,
  buildHyperliquidApproveAgentTypedData,
  buildHyperliquidApproveBuilderFeeAction,
  buildHyperliquidApproveBuilderFeeTypedData,
  authorizeHyperliquidAgent,
  hasApprovedHyperliquidBuilderFee,
  hyperliquidBuilderAddress,
  signHyperliquidAgentRequest,
} from '../src/agents/hyperliquid-agent.ts'
import { loadStoredTradingAgent, saveStoredTradingAgent, type StorageLike } from '../src/agents/storage.ts'
import { signWithStoredTradingAgent } from '../src/agents/signing.ts'
import type { StoredTradingAgent } from '../src/agents/types.ts'
import type { SigningRequest } from '../src/types/signing.ts'

const privateKey = '0x1111111111111111111111111111111111111111111111111111111111111111'
const agentAddress = '0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A'

test('Hyperliquid approveAgent typed data matches the selected-chain official fixture', async () => {
  const action = buildHyperliquidApproveAgentAction(agentAddress, 1, 1_748_970_123_456)
  const typedData = buildHyperliquidApproveAgentTypedData(action)

  assert.deepEqual(action, {
    type: 'approveAgent',
    hyperliquidChain: 'Mainnet',
    signatureChainId: '0x1',
    agentAddress,
    agentName: 'Orbital Markets',
    nonce: 1_748_970_123_456,
  })
  assert.deepEqual(typedData.domain, {
    name: 'HyperliquidSignTransaction',
    version: '1',
    chainId: 1,
    verifyingContract: '0x0000000000000000000000000000000000000000',
  })
  assert.equal(typedData.primaryType, 'HyperliquidTransaction:ApproveAgent')
  const owner = privateKeyToAccount('0x0123456789012345678901234567890123456789012345678901234567890123')
  assert.equal(
    await owner.signTypedData(typedData),
    '0x0afb2eeb78847d03955929890b3f6371feda37b33a14c7e7dbab45f433457c5667f8856879f44dcf164fd86c9fe58c084077aa5221ba31412951be7749e0351c1b',
  )
})

test('Hyperliquid builder approval uses the configured 2 bp maximum fee', () => {
  const action = buildHyperliquidApproveBuilderFeeAction(agentAddress, 1, 1_748_970_123_456)
  const typedData = buildHyperliquidApproveBuilderFeeTypedData(action)

  assert.deepEqual(action, {
    type: 'approveBuilderFee',
    hyperliquidChain: 'Mainnet',
    signatureChainId: '0x1',
    maxFeeRate: '0.02%',
    builder: agentAddress.toLowerCase(),
    nonce: 1_748_970_123_456,
  })
  assert.equal(typedData.primaryType, 'HyperliquidTransaction:ApproveBuilderFee')
})

test('a local Hyperliquid agent reproduces the official SDK L1 signature', async () => {
  const request = hyperliquidSigningRequest()
  const storage = new TestStorage()
  saveStoredTradingAgent(storage, hyperliquidAgent())
  const signed = await signWithStoredTradingAgent(storage, request)

  assert.equal(
    signed.signature,
    '0xb6049af5fc3b079c7b3ff1491a5233c5733e80ad7dc571e2f8be80e1f929d8a204c982410fc198181481578a77c4084bcba11d81cd9b6039023173046dd4b9c81c',
  )
  assert.equal(signed.signer_address, agentAddress)
  assert.equal(JSON.stringify(signed).includes(privateKey), false)
})

test('authorization relays no private key and persists only after venue acceptance', async () => {
  const storage = new TestStorage()
  let relayed = ''
  const agent = await authorizeHyperliquidAgent({
    storage,
    ownerAddress: '0x14791697260E4c9A71f18484C9f997B308e59325',
    chainId: 1,
    now: () => 1_748_970_123_456,
    builderFeeApproved: async () => true,
    signTypedData: async () => `0x${'1'.repeat(128)}1b`,
    signBuilderTypedData: async () => `0x${'2'.repeat(128)}1b`,
    relayBuilderApproval: async () => undefined,
    relay: async (request) => {
      relayed = JSON.stringify(request)
      assert.equal(storage.values.size, 0)
    },
  })

  assert.equal(relayed.includes(agent.privateKey), false)
  assert.equal(relayed.includes('private'), false)
  assert.equal(loadStoredTradingAgent(storage, 'hyperliquid', agent.ownerAddress)?.agentAddress, agent.agentAddress)
})

test('builder fee is approved before the Hyperliquid agent is persisted', async () => {
  const storage = new TestStorage()
  let builderApproved = false
  await authorizeHyperliquidAgent({
    storage,
    ownerAddress: '0x14791697260E4c9A71f18484C9f997B308e59325',
    chainId: 1,
    builderFeeApproved: async () => false,
    now: () => 1_748_970_123_456,
    signTypedData: async (typedData) => {
      assert.equal(typedData.message.nonce, 1_748_970_123_457n)
      return `0x${'1'.repeat(128)}1b`
    },
    signBuilderTypedData: async (typedData) => {
      assert.equal(typedData.message.maxFeeRate, '0.02%')
      return `0x${'2'.repeat(128)}1b`
    },
    relayBuilderApproval: async () => {
      assert.equal(storage.values.size, 0)
      builderApproved = true
    },
    relay: async () => assert.equal(builderApproved, true),
  })

  assert.equal(
    loadStoredTradingAgent(storage, 'hyperliquid', '0x14791697260E4c9A71f18484C9f997B308e59325')?.builderAddress,
    hyperliquidBuilderAddress,
  )
})

test('an existing 2 bp builder allowance skips the repeat wallet approval', async () => {
  const storage = new TestStorage()
  let builderPrompted = false
  await authorizeHyperliquidAgent({
    storage,
    ownerAddress: '0x14791697260E4c9A71f18484C9f997B308e59325',
    chainId: 1,
    builderFeeApproved: async () => true,
    now: () => 1_748_970_123_456,
    signTypedData: async (typedData) => {
      assert.equal(typedData.message.nonce, 1_748_970_123_456n)
      return `0x${'1'.repeat(128)}1b`
    },
    signBuilderTypedData: async () => {
      builderPrompted = true
      return `0x${'2'.repeat(128)}1b`
    },
    relayBuilderApproval: async () => { builderPrompted = true },
    relay: async () => undefined,
  })

  assert.equal(builderPrompted, false)
})

test('builder allowance lookup uses Hyperliquid tenths-of-a-basis-point units', async () => {
  const requestBodies: string[] = []
  const fetcher = async (_input: string | URL | Request, init?: RequestInit) => {
    requestBodies.push(String(init?.body))
    return new Response('20', { status: 200, headers: { 'content-type': 'application/json' } })
  }

  assert.equal(await hasApprovedHyperliquidBuilderFee(agentAddress, agentAddress, fetcher), true)
  assert.deepEqual(JSON.parse(requestBodies[0]), {
    type: 'maxBuilderFee', user: agentAddress, builder: agentAddress,
  })
})

test('builder allowance lookup fails closed without prompting for a signature', async () => {
  const fetcher = async () => new Response('unavailable', { status: 503 })

  await assert.rejects(
    hasApprovedHyperliquidBuilderFee(agentAddress, agentAddress, fetcher),
    /allowance request failed with 503/,
  )
})

test('a local Hyperliquid agent rejects payloads outside the L1 order policy', async () => {
  const request = hyperliquidSigningRequest()
  request.unsigned_payload = {
    ...request.unsigned_payload as object,
    domain: { name: 'HyperliquidSignTransaction', chainId: 1 },
  }

  await assert.rejects(signHyperliquidAgentRequest(request, hyperliquidAgent()), /not an allowed L1 order/)
})

test('a local Hyperliquid agent rejects an order that does not match its connection ID', async () => {
  const request = hyperliquidSigningRequest()
  const payload = request.unsigned_payload as { action: { orders: Array<{ s: string }> } }
  payload.action.orders[0].s = '3.000000'

  await assert.rejects(signHyperliquidAgentRequest(request, hyperliquidAgent()), /not an allowed L1 order/)
})

test('a local Hyperliquid agent signs only the prepared cross leverage update', async () => {
  const request = hyperliquidSigningRequest()
  const action = { type: 'updateLeverage', asset: 1, isCross: true, leverage: 2 } as const
  const nonce = 1_700_000_000_001
  const encoded = encode(action)
  const input = new Uint8Array(encoded.length + 9)
  input.set(encoded)
  new DataView(input.buffer).setBigUint64(encoded.length, BigInt(nonce), false)
  const connectionId = keccak256(input)
  request.id = 'leverage-1'
  request.client_order_id = 'leverage-1'
  request.action = 'update_leverage'
  request.leverage = 2
  request.unsigned_payload = {
    action,
    nonce,
    domain: {
      chainId: 1337,
      name: 'Exchange',
      verifyingContract: '0x0000000000000000000000000000000000000000',
      version: '1',
    },
    types: { Agent: [{ name: 'source', type: 'string' }, { name: 'connectionId', type: 'bytes32' }] },
    primaryType: 'Agent',
    message: { source: 'a', connectionId },
  }
  const signed = await signHyperliquidAgentRequest(request, hyperliquidAgent())
  assert.equal(signed.signer_address, agentAddress)

  ;(request.unsigned_payload as { action: { isCross: boolean } }).action.isCross = false
  await assert.rejects(signHyperliquidAgentRequest(request, hyperliquidAgent()), /not an allowed leverage update/)
})

function hyperliquidAgent(): StoredTradingAgent {
  return {
    version: 1,
    venue: 'hyperliquid',
    ownerAddress: '0x14791697260E4c9A71f18484C9f997B308e59325',
    agentAddress,
    privateKey,
    authorizedAt: '2026-08-10T12:00:00.000Z',
  }
}

function hyperliquidSigningRequest(): SigningRequest {
  return {
    id: 'request-1',
    client_order_id: 'client-1',
    venue: 'hyperliquid',
    action: 'open',
    account: '0x14791697260E4c9A71f18484C9f997B308e59325',
    signer: agentAddress,
    symbol: 'BTC',
    side: 'sell',
    amount: 2,
    price: 101.5 / 0.995,
    reduce_only: false,
    venue_asset_id: 1,
    unsigned_payload: {
      action: {
        type: 'order',
        orders: [{
          a: 1,
          b: false,
          p: '101.500000',
          s: '2.000000',
          r: false,
          t: { limit: { tif: 'Ioc' } },
          c: '0x00000000000000000000000000000001',
        }],
        grouping: 'na',
      },
      nonce: 1_700_000_000_000,
      domain: {
        chainId: 1337,
        name: 'Exchange',
        verifyingContract: '0x0000000000000000000000000000000000000000',
        version: '1',
      },
      types: {
        Agent: [
          { name: 'source', type: 'string' },
          { name: 'connectionId', type: 'bytes32' },
        ],
      },
      primaryType: 'Agent',
      message: {
        source: 'a',
        connectionId: '0x030c6c348229c1ba1f210242ad159bbb1e918c02f5addda9b179bff7086b7ba5',
      },
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
