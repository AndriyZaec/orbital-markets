import assert from 'node:assert/strict'
import test from 'node:test'

import { runAsterPrivateRequest } from '../src/agents/aster-private.ts'
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

function privateRequest(): SigningRequest {
  return {
    id: 'aster-get_position_mode-1', client_order_id: '', venue: 'aster',
    action: 'get_position_mode', account, signer: agent, symbol: '', side: '',
    amount: 0, price: 0, reduce_only: false,
    unsigned_payload: {}, venue_metadata: {},
    created_at: '2026-09-06T12:00:00Z', expires_at: '2099-09-06T12:00:30Z',
  }
}
