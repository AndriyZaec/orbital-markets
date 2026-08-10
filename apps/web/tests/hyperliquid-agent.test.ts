import assert from 'node:assert/strict'
import test from 'node:test'
import { privateKeyToAccount } from 'viem/accounts'

import {
  buildHyperliquidApproveAgentAction,
  buildHyperliquidApproveAgentTypedData,
  authorizeHyperliquidAgent,
  signHyperliquidAgentRequest,
} from '../src/agents/hyperliquid-agent.ts'
import { loadStoredTradingAgent, type StorageLike } from '../src/agents/storage.ts'
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

test('a local Hyperliquid agent reproduces the official SDK L1 signature', async () => {
  const request = hyperliquidSigningRequest()
  const signed = await signHyperliquidAgentRequest(request, hyperliquidAgent())

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
    signTypedData: async () => `0x${'1'.repeat(128)}1b`,
    relay: async (request) => {
      relayed = JSON.stringify(request)
      assert.equal(storage.values.size, 0)
    },
  })

  assert.equal(relayed.includes(agent.privateKey), false)
  assert.equal(relayed.includes('private'), false)
  assert.equal(loadStoredTradingAgent(storage, 'hyperliquid', agent.ownerAddress)?.agentAddress, agent.agentAddress)
})

test('a local Hyperliquid agent rejects payloads outside the L1 order policy', async () => {
  const request = hyperliquidSigningRequest()
  request.unsigned_payload = {
    ...request.unsigned_payload as object,
    domain: { name: 'HyperliquidSignTransaction', chainId: 1 },
  }

  await assert.rejects(signHyperliquidAgentRequest(request, hyperliquidAgent()), /not an allowed L1 order/)
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
    symbol: 'BTC',
    side: 'sell',
    amount: 2,
    price: 101.5,
    reduce_only: false,
    unsigned_payload: {
      action: { type: 'order' },
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
