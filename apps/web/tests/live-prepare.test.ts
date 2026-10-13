import assert from 'node:assert/strict'
import test from 'node:test'
import { prepareLive, type PrepareStage } from '../src/lib/live-prepare.ts'

function streamed(lines: string[]): Response {
  const encoder = new TextEncoder()
  return new Response(new ReadableStream({
    start(controller) {
      // Deliberately split JSON and UTF-8 across network chunks.
      const bytes = encoder.encode(lines.join('\n') + '\n')
      for (let i = 0; i < bytes.length; i += 7) controller.enqueue(bytes.slice(i, i + 7))
      controller.close()
    },
  }), { headers: { 'Content-Type': 'application/x-ndjson' } })
}

test('prepare reports real streamed stages and returns only the completed result', async () => {
  const original = globalThis.fetch
  const stages: PrepareStage[] = []
  globalThis.fetch = async (_input, init) => {
    assert.equal(new Headers(init?.headers).get('Accept'), 'application/x-ndjson')
    return streamed([
      JSON.stringify({ type: 'progress', stage: 'quotes' }),
      JSON.stringify({ type: 'progress', stage: 'accounts' }),
      JSON.stringify({ type: 'result', status: 200, body: { session_id: 'session-✓' } }),
    ])
  }
  try {
    assert.deepEqual(await prepareLive({}, (stage) => stages.push(stage)), { session_id: 'session-✓' })
    assert.deepEqual(stages, ['quotes', 'accounts'])
  } finally { globalThis.fetch = original }
})

test('streamed HTTP-200 transport does not hide a rejected execution result', async () => {
  const original = globalThis.fetch
  globalThis.fetch = async () => streamed([
    JSON.stringify({ type: 'progress', stage: 'liquidity' }),
    JSON.stringify({ type: 'result', status: 422, body: { error: 'Execution checks failed', reasons: ['fresh BBO depth unavailable'] } }),
  ])
  try {
    await assert.rejects(prepareLive({}, () => {}), /fresh BBO depth unavailable/)
  } finally { globalThis.fetch = original }
})

test('prepare supports a legacy JSON API and its detailed errors', async () => {
  const original = globalThis.fetch
  try {
    globalThis.fetch = async () => Response.json({ session_id: 'legacy' })
    assert.deepEqual(await prepareLive({}, () => {}), { session_id: 'legacy' })
    globalThis.fetch = async () => Response.json({ error: 'account state not ready', reasons: ['Aster: waiting for balance'] }, { status: 409 })
    await assert.rejects(prepareLive({}, () => {}), /Aster: waiting for balance/)
  } finally { globalThis.fetch = original }
})

test('prepare deadline aborts a stalled body and cancels its reader', async () => {
  const original = globalThis.fetch
  let signal: AbortSignal | null | undefined
  let canceled = false
  globalThis.fetch = async (_input, init) => {
    signal = init?.signal
    return new Response(new ReadableStream({
      start(controller) { controller.enqueue(new TextEncoder().encode('{"type":"progress","stage":"accounts"}\n')) },
      cancel() { canceled = true },
    }), { headers: { 'Content-Type': 'application/x-ndjson' } })
  }
  try {
    await assert.rejects(prepareLive({}, () => {}, 20), /Preparation timed out.*No orders were submitted/)
    assert.equal(signal?.aborted, true)
    assert.equal(canceled, true)
  } finally { globalThis.fetch = original }
})

test('prepare deadline also covers a request that never returns headers', async () => {
  const original = globalThis.fetch
  globalThis.fetch = async () => new Promise<Response>(() => {})
  try {
    await assert.rejects(prepareLive({}, () => {}, 20), /Preparation timed out/)
  } finally { globalThis.fetch = original }
})

test('a truncated stream never becomes a successful prepare', async () => {
  const original = globalThis.fetch
  globalThis.fetch = async () => streamed([JSON.stringify({ type: 'progress', stage: 'quotes' })])
  try {
    await assert.rejects(prepareLive({}, () => {}), /ended before completion/)
  } finally { globalThis.fetch = original }
})

test('prepare network failures give an actionable connection message', async () => {
  const original = globalThis.fetch
  globalThis.fetch = async () => { throw new TypeError('Failed to fetch') }
  try {
    await assert.rejects(prepareLive({}, () => {}), /Check your connection and try again/)
  } finally { globalThis.fetch = original }
})
