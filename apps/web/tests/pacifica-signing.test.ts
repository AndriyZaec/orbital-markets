import assert from 'node:assert/strict'
import test from 'node:test'

import { signPacifica } from '../src/lib/signing/pacifica.ts'

test('signPacifica signs Pacifica canonical order bytes', async () => {
  let signedMessage = ''
  const payload = {
    timestamp: 1_786_286_194_974,
    expiry_window: 120_000,
    symbol: 'XMR',
    side: 'ask',
    amount: '25',
    reduce_only: false,
    slippage_percent: '0.5',
    client_order_id: '9075cd5e-bd57-455f-b3b9-cf0f3f5b2f99',
  }

  await signPacifica(payload, async (message) => {
    signedMessage = new TextDecoder().decode(message)
    return new Uint8Array(64)
  })

  assert.equal(
    signedMessage,
    '{"data":{"amount":"25","client_order_id":"9075cd5e-bd57-455f-b3b9-cf0f3f5b2f99","reduce_only":false,"side":"ask","slippage_percent":"0.5","symbol":"XMR"},"expiry_window":120000,"timestamp":1786286194974,"type":"create_market_order"}',
  )
})
