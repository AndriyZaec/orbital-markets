import assert from 'node:assert/strict'
import test from 'node:test'

import {
  liveAccountsKey,
  liveAccountsQuery,
  liveVenueBindingsBody,
} from '../src/lib/live-bindings.ts'

const accounts = { pacifica: 'sol-owner', hyperliquid: '0xAbC' }
const agents = { pacifica: 'sol-agent', hyperliquid: '0xDeF' }

test('live binding bodies dual-write venue maps and legacy aliases', () => {
  assert.deepEqual(liveVenueBindingsBody(accounts, agents), {
    accounts,
    agents,
    account_pacifica: 'sol-owner',
    account_hyperliquid: '0xAbC',
    agent_pacifica: 'sol-agent',
    agent_hyperliquid: '0xDeF',
  })
})

test('live binding bodies omit absent optional agents for close reconciliation', () => {
  assert.deepEqual(liveVenueBindingsBody(accounts, { pacifica: '', hyperliquid: '' }), {
    accounts,
    account_pacifica: 'sol-owner',
    account_hyperliquid: '0xAbC',
  })
})

test('live account queries are deterministic and retain legacy aliases', () => {
  assert.equal(
    liveAccountsQuery(accounts, { session_id: 'session-1' }).toString(),
    'accounts%5Bhyperliquid%5D=0xAbC&accounts%5Bpacifica%5D=sol-owner&account_pacifica=sol-owner&account_hyperliquid=0xAbC&session_id=session-1',
  )
  assert.equal(
    liveAccountsKey(accounts),
    liveAccountsKey({ hyperliquid: '0xabc', pacifica: 'sol-owner' }),
  )
})
