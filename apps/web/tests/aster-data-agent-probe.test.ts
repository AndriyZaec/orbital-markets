import assert from 'node:assert/strict'
import test from 'node:test'
import { privateKeyToAccount } from 'viem/accounts'

import {
  buildAsterDataAgentApprovalTypedData,
  parseAsterDataAgentPreparation,
  parseAsterDataAgentReport,
  parseAsterDataAgentStatus,
  runAsterDataAgentProbe,
} from '../src/agents/aster-data-agent-probe.ts'

const owner = privateKeyToAccount('0x0123456789012345678901234567890123456789012345678901234567890123')
const executionAgent = '0x2222222222222222222222222222222222222222'
const dataAgent = '0x3333333333333333333333333333333333333333'

function preparation() {
  return {
    probe_id: 'probe-1',
    approval: {
      user: owner.address,
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

test('validates every read-only approval field and builds exact Aster typed data without builder fields', () => {
  const prepared = parseAsterDataAgentPreparation(preparation(), owner.address)
  const typedData = buildAsterDataAgentApprovalTypedData(prepared.approval)

  assert.deepEqual(Object.keys(prepared), ['probe_id', 'approval'])
  assert.deepEqual(Object.keys(prepared.approval), [
    'user', 'nonce', 'agentName', 'agentAddress', 'ipWhitelist', 'expired',
    'canSpotTrade', 'canPerpTrade', 'canWithdraw', 'asterChain', 'signatureChainId',
  ])
  assert.deepEqual(Object.keys(typedData), ['domain', 'types', 'primaryType', 'message'])
  assert.deepEqual(Object.keys(typedData.types), ['EIP712Domain', 'ApproveAgent'])
  assert.deepEqual(Object.keys(typedData.message), [
    'AgentName', 'AgentAddress', 'IpWhitelist', 'Expired', 'CanSpotTrade',
    'CanPerpTrade', 'CanWithdraw', 'AsterChain', 'User', 'Nonce',
  ])
  assert.equal(typedData.domain.chainId, 56n)
  assert.equal('builder' in typedData.message, false)
})

test('approved probe status reuses the backend agent without an owner signature', async () => {
  const originalFetch = globalThis.fetch
  const calls: Array<{ path: string; body: unknown }> = []
  let signed = false
  globalThis.fetch = async (input, init) => {
    calls.push({ path: String(input), body: init?.body ? JSON.parse(String(init.body)) : null })
    if (calls.length === 1) return Response.json({
      status: 'approved', agent_address: dataAgent,
      requested_expiry: preparation().approval.expired,
    })
    return Response.json(successReport(preparation().approval.expired))
  }
  try {
    const result = await runAsterDataAgentProbe({
      account: owner.address,
      executionAgent,
      switchChain: async () => {},
      signTypedData: async () => {
        signed = true
        throw new Error('must not sign')
      },
      isCurrent: () => true,
    })
    assert.equal(signed, false)
    assert.equal(result.agent_address, dataAgent)
    assert.deepEqual(calls, [
      {
        path: `/api/v1/live/aster/data-agent/status?account=${encodeURIComponent(owner.address)}&execution_agent=${encodeURIComponent(executionAgent)}`,
        body: null,
      },
      {
        path: '/api/v1/live/aster/data-agent/run',
        body: { account: owner.address, execution_agent: executionAgent },
      },
    ])
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('rejects altered or extra approval fields before wallet signing', () => {
  assert.throws(
    () => parseAsterDataAgentPreparation({
      ...preparation(),
      approval: { ...preparation().approval, canPerpTrade: true },
    }, owner.address),
    /invalid Aster read-only approval/,
  )
  assert.throws(
    () => parseAsterDataAgentPreparation({ ...preparation(), private_key: 'secret' }, owner.address),
    /invalid Aster data-agent preparation response/,
  )
  assert.throws(
    () => parseAsterDataAgentPreparation({
      ...preparation(), approval: { ...preparation().approval, user: executionAgent },
    }, owner.address),
    /mismatched Aster approval owner/,
  )
})

test('strictly parses status and its correlated last report', () => {
  const value = {
    status: 'approved', agent_address: dataAgent,
    requested_expiry: preparation().approval.expired,
    last_result: successReport(preparation().approval.expired),
  }
  assert.equal(parseAsterDataAgentStatus(value).last_result?.success, true)
  assert.throws(() => parseAsterDataAgentStatus({ ...value, private_key: 'secret' }), /invalid Aster data-agent status/)
  assert.throws(() => parseAsterDataAgentStatus({ ...value, status: 'ready' }), /invalid Aster data-agent status/)
  assert.throws(() => parseAsterDataAgentStatus({
    ...value,
    last_result: successReport(preparation().approval.expired + 1),
  }), /invalid Aster data-agent probe result/)
})

test('strictly correlates a report with status metadata without inventing a probe id', () => {
  const result = parseAsterDataAgentReport(successReport(preparation().approval.expired), {
    agentAddress: dataAgent,
    requestedExpiry: preparation().approval.expired,
  })
  assert.equal('probe_id' in result, false)
  assert.equal(result.agent_address, dataAgent)
  assert.throws(() => parseAsterDataAgentReport(successReport(preparation().approval.expired), {
    agentAddress: dataAgent,
    requestedExpiry: preparation().approval.expired + 1,
  }), /invalid Aster data-agent probe result/)
})

test('probe prepares, signs on BSC, verifies locally, and validates without replacing execution agent', async () => {
  const originalFetch = globalThis.fetch
  const calls: Array<{ path: string; body: unknown }> = []
  const phases: string[] = []
  let switchChainId = 0
  globalThis.fetch = async (input, init) => {
    calls.push({ path: String(input), body: init?.body ? JSON.parse(String(init.body)) : null })
    if (calls.length === 1) return Response.json({}, { status: 404 })
    if (calls.length === 2) return Response.json(preparation())
    return Response.json({
      endpoints: { agent: true, account: true, position_risk: true, income: true },
      matched_agent_permissions: {
        canRead: true, canSpotTrade: false, canPerpTrade: false, canWithdraw: false,
      },
      requested_expiry: preparation().approval.expired,
      reported_expiry: preparation().approval.expired - 30_000,
      execution_agent_preserved: true,
      success: true,
    })
  }
  try {
    const result = await runAsterDataAgentProbe({
      account: owner.address,
      executionAgent,
      switchChain: async (chainId) => { switchChainId = chainId },
      signTypedData: (typedData) => owner.signTypedData(typedData),
      isCurrent: () => true,
      onPhase: (phase) => phases.push(phase),
    })

    assert.equal(switchChainId, 56)
    assert.deepEqual(phases, ['checking_status', 'preparing', 'wallet_signature', 'validating_reads'])
    assert.deepEqual(calls.map((call) => call.path), [
      `/api/v1/live/aster/data-agent/status?account=${encodeURIComponent(owner.address)}&execution_agent=${encodeURIComponent(executionAgent)}`,
      '/api/v1/live/aster/data-agent/prepare',
      '/api/v1/live/aster/data-agent/validate',
    ])
    assert.deepEqual(calls[1].body, { account: owner.address, execution_agent: executionAgent })
    assert.deepEqual(Object.keys(calls[2].body as object), ['probe_id', 'signature', 'account', 'execution_agent'])
    assert.equal(result.execution_agent_preserved, true)
    assert.equal(result.endpoint_checks.income, true)
    assert.equal(result.expired, preparation().approval.expired - 30_000)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('probe stops before status if the execution authorization is no longer current', async () => {
  const originalFetch = globalThis.fetch
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    return Response.json(preparation())
  }
  try {
    await assert.rejects(runAsterDataAgentProbe({
      account: owner.address,
      executionAgent,
      switchChain: async () => {},
      signTypedData: async (typedData) => owner.signTypedData(typedData),
      isCurrent: () => false,
    }), /owner or execution agent changed/)
    assert.equal(calls, 0)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('probe reports the exact failed read capability', async () => {
  const originalFetch = globalThis.fetch
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    if (calls === 1) return Response.json({
      status: 'approved', agent_address: dataAgent,
      requested_expiry: preparation().approval.expired,
    })
    return Response.json({
      endpoints: { agent: true, account: true, position_risk: false, income: false },
      matched_agent_permissions: {
        canRead: true, canSpotTrade: false, canPerpTrade: false, canWithdraw: false,
      },
      requested_expiry: preparation().approval.expired,
      reported_expiry: preparation().approval.expired,
      execution_agent_preserved: true,
      success: false,
      error: 'Aster position-risk read failed',
    })
  }
  try {
    await assert.rejects(runAsterDataAgentProbe({
      account: owner.address,
      executionAgent,
      switchChain: async () => {},
      signTypedData: (typedData) => owner.signTypedData(typedData),
      isCurrent: () => true,
    }), /Aster position-risk read failed/)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('uncertain and rejected statuses stop without signing or retrying', async () => {
  const originalFetch = globalThis.fetch
  try {
    for (const status of ['uncertain', 'rejected'] as const) {
      let calls = 0
      let signed = false
      globalThis.fetch = async () => {
        calls += 1
        return Response.json({
          status, agent_address: dataAgent,
          requested_expiry: preparation().approval.expired,
          last_error: `Aster ${status} status requires operator review`,
        })
      }
      await assert.rejects(runAsterDataAgentProbe({
        account: owner.address,
        executionAgent,
        switchChain: async () => {},
        signTypedData: async () => {
          signed = true
          throw new Error('must not sign')
        },
        isCurrent: () => true,
      }), new RegExp(`Aster ${status} status requires operator review`))
      assert.equal(calls, 1)
      assert.equal(signed, false)
    }
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('approved status with a failed prior run safely retries reads without signing', async () => {
  const originalFetch = globalThis.fetch
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    if (calls === 1) return Response.json({
      status: 'approved', agent_address: dataAgent,
      requested_expiry: preparation().approval.expired,
      last_result: {
        endpoints: { agent: true, account: false, position_risk: false, income: false },
        requested_expiry: preparation().approval.expired,
        execution_agent_preserved: true,
        success: false,
        error: 'Aster account read failed',
      },
    })
    return Response.json(successReport(preparation().approval.expired))
  }
  try {
    await runAsterDataAgentProbe({
      account: owner.address,
      executionAgent,
      switchChain: async () => {},
      signTypedData: async () => { throw new Error('must not sign') },
      isCurrent: () => true,
    })
    assert.equal(calls, 2)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('expired pending status starts a replacement approval flow', async () => {
  const originalFetch = globalThis.fetch
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    if (calls === 1) return Response.json({
      status: 'pending', agent_address: dataAgent,
      requested_expiry: preparation().approval.expired,
    })
    if (calls === 2) return Response.json(preparation())
    return Response.json(successReport(preparation().approval.expired))
  }
  try {
    await runAsterDataAgentProbe({
      account: owner.address,
      executionAgent,
      switchChain: async () => {},
      signTypedData: (typedData) => owner.signTypedData(typedData),
      isCurrent: () => true,
      now: () => Math.floor(preparation().approval.nonce / 1000) + 60_001,
    })
    assert.equal(calls, 3)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('unexpired pending status stops without creating another probe', async () => {
  const originalFetch = globalThis.fetch
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    return Response.json({
      status: 'pending', agent_address: dataAgent,
      requested_expiry: preparation().approval.expired,
    })
  }
  try {
    await assert.rejects(runAsterDataAgentProbe({
      account: owner.address,
      executionAgent,
      switchChain: async () => {},
      signTypedData: async () => { throw new Error('must not sign') },
      isCurrent: () => true,
      now: () => Math.floor(preparation().approval.nonce / 1000) + 59_999,
    }), /approval is pending/)
    assert.equal(calls, 1)
  } finally {
    globalThis.fetch = originalFetch
  }
})

function successReport(requestedExpiry: number) {
  return {
    endpoints: { agent: true, account: true, position_risk: true, income: true },
    matched_agent_permissions: {
      canRead: true, canSpotTrade: false, canPerpTrade: false, canWithdraw: false,
    },
    requested_expiry: requestedExpiry,
    reported_expiry: requestedExpiry,
    execution_agent_preserved: true,
    success: true,
  }
}
