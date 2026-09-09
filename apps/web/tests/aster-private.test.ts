import assert from 'node:assert/strict'
import test from 'node:test'

import { refreshAsterAccountSnapshot, runAsterPrivateRequest } from '../src/agents/aster-private.ts'
import type { SigningRequest } from '../src/types/signing.ts'

const account = '0x1111111111111111111111111111111111111111'
const agent = '0x2222222222222222222222222222222222222222'

test('Aster private request uses the prepared request and correlated response', async () => {
  const originalFetch = globalThis.fetch
  const calls: Array<{ path: string; body: string }> = []
  const request = privateRequest()
  globalThis.fetch = async (input, init) => {
    calls.push({ path: String(input), body: String(init?.body ?? '') })
    if (calls.length === 1) return Response.json(request)
    return Response.json({
      request_id: request.id,
      operation: 'get_position_mode',
      data: { dualSidePosition: false },
    })
  }
  try {
    const result = await runAsterPrivateRequest<{ dualSidePosition: boolean }>(
      { operation: 'get_position_mode', account, agent },
      async (prepared) => ({
        request_id: prepared.id, client_order_id: prepared.client_order_id,
        venue: 'aster', signer_address: agent, signature: `0x${'1'.repeat(130)}`,
      }),
      () => true,
    )
    assert.deepEqual(result, { dualSidePosition: false })
    assert.deepEqual(calls.map((call) => call.path), [
      '/api/v1/live/aster/private/prepare', '/api/v1/live/aster/private/submit',
    ])
    assert.equal(calls[0].body.includes('private'), false)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('Aster private request rejects substituted prepare parameters before signing', async () => {
  const originalFetch = globalThis.fetch
  const request = {
    ...privateRequest(), action: 'update_leverage', symbol: 'ETHUSDT', leverage: 20,
  }
  let signed = false
  globalThis.fetch = async () => Response.json(request)
  try {
    await assert.rejects(
      runAsterPrivateRequest(
        { operation: 'update_leverage', account, agent, symbol: 'BTCUSDT', leverage: 5 },
        async () => {
          signed = true
          throw new Error('must not sign')
        },
        () => true,
      ),
      /mismatched Aster signing request/,
    )
    assert.equal(signed, false)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('Aster private request stops when the wallet changes during signing', async () => {
  const originalFetch = globalThis.fetch
  const request = privateRequest()
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    return Response.json(request)
  }
  try {
    await assert.rejects(
      runAsterPrivateRequest(
        { operation: 'get_position_mode', account, agent },
        async (prepared) => ({
          request_id: prepared.id, client_order_id: '', venue: 'aster',
          signer_address: agent, signature: `0x${'1'.repeat(130)}`,
        }),
        () => false,
      ),
      /owner or authorization changed/,
    )
    assert.equal(calls, 1)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('Aster private request surfaces an uncertain outcome without returning data', async () => {
  const originalFetch = globalThis.fetch
  const request = privateRequest()
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    if (calls === 1) return Response.json(request)
    return Response.json({
      request_id: request.id, operation: request.action, uncertain: true,
      error: 'outcome unknown',
    }, { status: 202 })
  }
  try {
    await assert.rejects(
      runAsterPrivateRequest(
        { operation: 'get_position_mode', account, agent },
        async (prepared) => ({
          request_id: prepared.id, client_order_id: '', venue: 'aster',
          signer_address: agent, signature: `0x${'1'.repeat(130)}`,
        }),
        () => true,
      ),
      /outcome unknown/,
    )
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('Aster account refresh signs and submits one coherent three-part snapshot', async () => {
  const originalFetch = globalThis.fetch
  const operations = ['get_position_mode', 'get_account', 'get_positions'] as const
  const requests = operations.map(snapshotRequest)
  const signed: string[] = []
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    if (calls === 1) return Response.json({ snapshot_id: 'snapshot-1', requests })
    const operation = operations[calls - 2]
    return Response.json({
      request_id: requests[calls - 2].id, operation, data: {}, state_applied: true,
    })
  }
  try {
    const status = await refreshAsterAccountSnapshot(account, agent, async (request) => {
      signed.push(request.action)
      return {
        request_id: request.id, client_order_id: '', venue: 'aster',
        signer_address: agent, signature: `0x${'1'.repeat(130)}`,
      }
    }, () => true)
    assert.deepEqual(signed, operations)
    assert.equal(calls, 4)
    assert.equal(status, 'ready')
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('Aster account refresh stops after deposit-required response', async () => {
  const originalFetch = globalThis.fetch
  const operations = ['get_position_mode', 'get_account', 'get_positions'] as const
  const requests = operations.map(snapshotRequest)
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    if (calls === 1) return Response.json({ snapshot_id: 'snapshot-1', requests })
    return Response.json({
      request_id: requests[0].id,
      operation: operations[0],
      data: { depositRequired: true },
      deposit_required: true,
      state_applied: true,
    })
  }
  try {
    const status = await refreshAsterAccountSnapshot(account, agent, async (request) => ({
      request_id: request.id, client_order_id: '', venue: 'aster',
      signer_address: agent, signature: `0x${'1'.repeat(130)}`,
    }), () => true)
    assert.equal(status, 'deposit_required')
    assert.equal(calls, 2)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('Aster lightweight account refresh omits position mode', async () => {
  const originalFetch = globalThis.fetch
  const operations = ['get_account', 'get_positions'] as const
  const requests = operations.map(snapshotRequest)
  const bodies: string[] = []
  let calls = 0
  globalThis.fetch = async (_input, init) => {
    calls += 1
    bodies.push(String(init?.body ?? ''))
    if (calls === 1) return Response.json({ snapshot_id: 'snapshot-1', requests })
    const operation = operations[calls - 2]
    return Response.json({
      request_id: requests[calls - 2].id, operation, data: {}, state_applied: true,
    })
  }
  try {
    const status = await refreshAsterAccountSnapshot(account, agent, async (request) => ({
      request_id: request.id, client_order_id: '', venue: 'aster',
      signer_address: agent, signature: `0x${'1'.repeat(130)}`,
    }), () => true, true)
    assert.equal(status, 'ready')
    assert.equal(calls, 3)
    assert.equal(JSON.parse(bodies[0]).refresh_only, true)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('Aster account refresh rejects mixed snapshot generations before signing', async () => {
  const originalFetch = globalThis.fetch
  const requests = [
    snapshotRequest('get_position_mode'),
    { ...snapshotRequest('get_account'), snapshot_id: 'snapshot-2' },
    snapshotRequest('get_positions'),
  ]
  let signed = false
  globalThis.fetch = async () => Response.json({ snapshot_id: 'snapshot-1', requests })
  try {
    await assert.rejects(
      refreshAsterAccountSnapshot(account, agent, async () => {
        signed = true
        throw new Error('must not sign')
      }, () => true),
      /incoherent Aster account snapshot/,
    )
    assert.equal(signed, false)
  } finally {
    globalThis.fetch = originalFetch
  }
})

function privateRequest(): SigningRequest {
  return {
    id: 'aster-get_position_mode-1', client_order_id: '', venue: 'aster',
    action: 'get_position_mode', account, signer: agent, symbol: '', side: '',
    amount: 0, price: 0, reduce_only: false,
    unsigned_payload: {}, venue_metadata: {},
    created_at: '2026-09-06T12:00:00Z', expires_at: '2099-09-06T12:00:30Z',
  }
}

function snapshotRequest(action: SigningRequest['action']): SigningRequest {
  return {
    ...privateRequest(), id: `aster-${action}-1`, snapshot_id: 'snapshot-1', action,
  }
}
