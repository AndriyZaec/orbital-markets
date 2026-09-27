import assert from 'node:assert/strict'
import test from 'node:test'

import { waitForClosedPosition } from '../src/lib/live-close.ts'
import { submitSignedActionsConcurrently } from '../src/lib/signed-submissions.ts'
import type { SignedAction, SigningRequest, SubmissionResult } from '../src/types/signing.ts'

const signingRequest = (id: string): SigningRequest => ({
  id,
  client_order_id: `order-${id}`,
  venue: id === '1' ? 'pacifica' : 'hyperliquid',
  action: 'close',
  account: 'account',
  symbol: 'SOL',
  side: 'sell',
  amount: 1,
  price: 0,
  reduce_only: true,
  unsigned_payload: {},
  expires_at: new Date(Date.now() + 30_000).toISOString(),
  created_at: new Date().toISOString(),
})

const signedAction = (id: string): SignedAction => ({
  request_id: id,
  client_order_id: `order-${id}`,
  venue: id === '1' ? 'pacifica' : 'hyperliquid',
  signer_address: 'signer',
  signature: 'signature',
})

test('close confirmation tolerates a transient degraded state before reconciliation closes the position', async () => {
  const states = ['degraded', 'closed']
  let reads = 0

  await waitForClosedPosition({
    getPositionState: async () => states[reads++] ?? 'closed',
    delay: async () => {},
    attempts: 3,
    pollMs: 0,
  })

  assert.equal(reads, 2)
})

test('close confirmation still reports degraded exposure after the polling window', async () => {
  let reads = 0

  await assert.rejects(
    waitForClosedPosition({
      getPositionState: async () => {
        reads++
        return 'degraded'
      },
      delay: async () => {},
      attempts: 3,
      pollMs: 0,
    }),
    /manual action may be required/,
  )

  assert.equal(reads, 3)
})

test('starts every signed close submission before waiting for the slowest venue', async () => {
  let releasePacifica!: (result: SubmissionResult) => void
  const pacificaResult = new Promise<SubmissionResult>((resolve) => {
    releasePacifica = resolve
  })
  const started: string[] = []
  const actions = ['1', '2'].map((id) => ({
    request: signingRequest(id),
    signed: signedAction(id),
  }))

  const submissions = submitSignedActionsConcurrently(actions, async (signed) => {
    started.push(signed.venue)
    if (signed.venue === 'pacifica') return pacificaResult
    return {
      request_id: signed.request_id,
      client_order_id: signed.client_order_id,
      venue: signed.venue,
      accepted: true,
      submitted_at: '',
      responded_at: '',
    }
  })

  await Promise.resolve()
  assert.deepEqual(started, ['pacifica', 'hyperliquid'])

  releasePacifica({
    request_id: '1',
    client_order_id: 'order-1',
    venue: 'pacifica',
    accepted: true,
    submitted_at: '',
    responded_at: '',
  })
  const settled = await submissions
  assert.deepEqual(settled.map(({ request }) => request.id), ['1', '2'])
  assert.ok(settled.every(({ outcome }) => outcome.status === 'fulfilled'))
})
