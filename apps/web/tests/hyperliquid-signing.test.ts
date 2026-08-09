import assert from 'node:assert/strict'
import test from 'node:test'

import { signHyperliquid } from '../src/lib/signing/hyperliquid.ts'

test('signHyperliquid forwards the backend Agent typed data', async () => {
  const payload = {
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
  }
  let signed: unknown
  const signature = await signHyperliquid(payload, async (typed) => {
    signed = typed
    return '0xsigned'
  })

  assert.equal(signature, '0xsigned')
  assert.deepEqual(signed, {
    domain: payload.domain,
    types: payload.types,
    primaryType: 'Agent',
    message: payload.message,
  })
})

test('signHyperliquid rejects raw actions before calling the wallet', async () => {
  let called = false
  await assert.rejects(
    signHyperliquid({ action: { type: 'order' }, nonce: 1 }, async () => {
      called = true
      return '0xsigned'
    }),
    /Invalid Hyperliquid EIP-712 signing payload/,
  )
  assert.equal(called, false)
})
