import test from 'node:test'
import assert from 'node:assert/strict'
import { accountRead } from '../src/lib/account-read.ts'

test('a stalled read body releases account work at the deadline', async () => {
  let signal: AbortSignal | undefined
  await assert.rejects(accountRead(async (current) => {
    signal = current
    return new Promise<never>(() => {})
  }, undefined, 10), /refresh timed out/)
  assert.equal(signal?.aborted, true)
  assert.equal(await accountRead(async () => 42), 42)
})

test('owner cancellation interrupts a read even if the underlying provider ignores it', async () => {
  const owner = new AbortController()
  const read = accountRead(async () => new Promise<never>(() => {}), owner.signal)
  owner.abort()
  await assert.rejects(read, { name: 'AbortError' })
  await assert.rejects(accountRead(async () => { throw new Error('must not start') }, owner.signal), { name: 'AbortError' })
})
