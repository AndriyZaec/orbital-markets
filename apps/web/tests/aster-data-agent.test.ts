import assert from 'node:assert/strict'
import test from 'node:test'

import {
  authorizeAsterDataAgent,
  buildAsterDataAgentApprovalTypedData,
  parseAsterDataAgentPreparation,
  prepareAsterDataAgentAuthorization,
} from '../src/agents/aster-data-agent.ts'

const owner = '0x1111111111111111111111111111111111111111'
const executionAgent = '0x2222222222222222222222222222222222222222'
const dataAgent = '0x3333333333333333333333333333333333333333'

function preparation() {
  return {
    probe_id: 'authorization-1',
    approval: {
      user: owner,
      nonce: 1_799_999_999_000_000,
      agentName: 'OrbitalData',
      agentAddress: dataAgent,
      ipWhitelist: '',
      expired: 1_831_535_999_000,
      canSpotTrade: false,
      canPerpTrade: false,
      canWithdraw: false,
      asterChain: 'Mainnet',
      signatureChainId: 56,
    },
  }
}

test('validates the read-only approval and builds exact typed data', () => {
  const prepared = parseAsterDataAgentPreparation(preparation(), owner)
  const typedData = buildAsterDataAgentApprovalTypedData(prepared.approval)

  assert.equal(typedData.primaryType, 'ApproveAgent')
  assert.equal(typedData.domain.chainId, 56n)
  assert.equal('builder' in typedData.message, false)
  assert.throws(() => parseAsterDataAgentPreparation({
    ...preparation(), approval: { ...preparation().approval, canPerpTrade: true },
  }, owner), /invalid Aster read-only approval/)
})

test('prepares and submits read-only authorization without browser read probes', async () => {
  const originalFetch = globalThis.fetch
  const calls: Array<{ path: string; body: unknown }> = []
  globalThis.fetch = async (input, init) => {
    calls.push({ path: String(input), body: init?.body ? JSON.parse(String(init.body)) : null })
    return calls.length === 1 ? Response.json(preparation()) : Response.json({})
  }
  try {
    const prepared = await prepareAsterDataAgentAuthorization({
      account: owner, executionAgent, isCurrent: () => true,
    })
    await authorizeAsterDataAgent({
      account: owner,
      executionAgent,
      preparation: prepared,
      signature: `0x${'1'.repeat(130)}`,
      isCurrent: () => true,
    })
    assert.deepEqual(calls.map((call) => call.path), [
      '/api/v1/live/aster/data-agent/prepare',
      '/api/v1/live/aster/data-agent/authorize',
    ])
    assert.deepEqual(calls[0].body, { account: owner, execution_agent: executionAgent })
    assert.deepEqual(Object.keys(calls[1].body as object), [
      'probe_id', 'signature', 'account', 'execution_agent', 'approval',
    ])
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('authorization stops before a request when the owner changes', async () => {
  const originalFetch = globalThis.fetch
  let called = false
  globalThis.fetch = async () => {
    called = true
    return Response.json(preparation())
  }
  try {
    await assert.rejects(prepareAsterDataAgentAuthorization({
      account: owner, executionAgent, isCurrent: () => false,
    }), /changed during authorization/)
    assert.equal(called, false)
  } finally {
    globalThis.fetch = originalFetch
  }
})
