import assert from 'node:assert/strict'
import test from 'node:test'
import { privateKeyToAccount } from 'viem/accounts'

import {
  authorizeAsterAgent,
  buildAsterApproveAgentAction,
  buildAsterApproveAgentTypedData,
  signAsterAgentRequest,
} from '../src/agents/aster-agent.ts'
import { loadStoredTradingAgent, type StorageLike } from '../src/agents/storage.ts'
import { signWithStoredTradingAgent } from '../src/agents/signing.ts'
import type { StoredTradingAgent } from '../src/agents/types.ts'
import type { SigningRequest } from '../src/types/signing.ts'

const ownerAddress = '0x14791697260E4c9A71f18484C9f997B308e59325'
const privateKey = '0x1111111111111111111111111111111111111111111111111111111111111111'
const agentAddress = '0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A'

test('Aster approval is perpetual-only and signed on BSC', async () => {
  const action = buildAsterApproveAgentAction(ownerAddress, agentAddress, 1_748_970_123_456)
  const typedData = buildAsterApproveAgentTypedData(action)

  assert.equal(action.nonce, 1_748_970_123_456_000)
  assert.equal(action.canSpotTrade, false)
  assert.equal(action.canPerpTrade, true)
  assert.equal(action.canWithdraw, false)
  assert.equal(action.signatureChainId, 56)
  assert.equal(typedData.domain.chainId, 56)
  assert.equal(typedData.primaryType, 'ApproveAgent')
  const owner = privateKeyToAccount('0x0123456789012345678901234567890123456789012345678901234567890123')
  assert.equal(
    await owner.signTypedData(typedData),
    '0x23ab0a37ca52e98ab7f191e36179dd341d04dc2f0e95b5366242f05bd60932040c2899ab8993e48a1a5030673ad2f7eda8776341aebef96cb2e022645cc4d7511b',
  )
})

test('Aster authorization relays no private key and persists only after acceptance', async () => {
  const storage = new TestStorage()
  let relayed = ''
  const now = Date.now()
  const agent = await authorizeAsterAgent({
    storage,
    ownerAddress,
    now: () => now,
    signTypedData: async () => `0x${'1'.repeat(128)}1b`,
    relay: async (request) => {
      relayed = JSON.stringify(request)
      assert.equal(storage.values.size, 0)
    },
  })

  assert.equal(relayed.includes(agent.privateKey), false)
  assert.equal(relayed.includes('private'), false)
  assert.equal(loadStoredTradingAgent(storage, 'aster', ownerAddress)?.agentAddress, agent.agentAddress)
})

test('Aster authorization does not relay after the owner changes', async () => {
  let relayed = false
  await assert.rejects(authorizeAsterAgent({
    storage: new TestStorage(),
    ownerAddress,
    signTypedData: async () => `0x${'1'.repeat(128)}1b`,
    ownerStillCurrent: () => false,
    relay: async () => { relayed = true },
  }), /owner changed/)
  assert.equal(relayed, false)
})

test('a local Aster agent signs the exact allowed IOC payload', async () => {
  const storage = new TestStorage()
  storage.setItem(
    `orbital.agent.aster.v1:${ownerAddress.toLowerCase()}`,
    JSON.stringify(asterAgent()),
  )

  const signed = await signWithStoredTradingAgent(storage, asterSigningRequest())

  assert.match(signed.signature, /^0x[0-9a-f]{130}$/)
  assert.equal(signed.signer_address, agentAddress)
  assert.equal(JSON.stringify(signed).includes(privateKey), false)
})

test('Aster signing rejects altered or duplicated order parameters', async () => {
  const request = asterSigningRequest()
  const payload = request.unsigned_payload as { message: { msg: string } }
  payload.message.msg = payload.message.msg.replace('price=100.5', 'price=101')
  await assert.rejects(signAsterAgentRequest(request, asterAgent()), /not an allowed IOC order/)

  const duplicate = asterSigningRequest()
  const duplicatePayload = duplicate.unsigned_payload as { message: { msg: string } }
  duplicatePayload.message.msg += '&symbol=ETHUSDT'
  await assert.rejects(signAsterAgentRequest(duplicate, asterAgent()), /duplicate query parameters/)
})

test('Aster signing rejects builder fields until attribution is enabled', async () => {
  const request = asterSigningRequest()
  const payload = request.unsigned_payload as { message: { msg: string } }
  payload.message.msg = payload.message.msg.replace('&side=BUY', '&builder=0x3333333333333333333333333333333333333333&side=BUY')
  await assert.rejects(signAsterAgentRequest(request, asterAgent()), /not an allowed IOC order/)
})

test('Aster agent signs only allowlisted private account requests', async () => {
  const operations: SigningRequest['action'][] = [
    'get_position_mode', 'get_account', 'get_positions', 'get_leverage_brackets',
    'query_order', 'update_leverage', 'start_user_stream', 'keepalive_user_stream', 'close_user_stream',
  ]
  for (const operation of operations) {
    const signed = await signAsterAgentRequest(asterPrivateSigningRequest(operation), asterAgent())
    assert.match(signed.signature, /^0x[0-9a-f]{130}$/)
  }
})

test('Aster agent rejects private request fields outside the operation policy', async () => {
  const request = asterPrivateSigningRequest('get_account')
  const payload = request.unsigned_payload as { message: { msg: string } }
  payload.message.msg = `withdraw=true&${payload.message.msg}`
  await assert.rejects(signAsterAgentRequest(request, asterAgent()), /not an allowed private request/)
})

function asterAgent(): StoredTradingAgent {
  assert.equal(privateKeyToAccount(privateKey).address.toLowerCase(), agentAddress.toLowerCase())
  return {
    version: 1,
    venue: 'aster',
    ownerAddress,
    agentAddress,
    privateKey,
    authorizedAt: '2026-08-10T12:00:00.000Z',
    expiresAt: '2099-08-10T12:00:00.000Z',
  }
}

function asterSigningRequest(): SigningRequest {
  const message = [
    'symbol=BTCUSDT',
    'type=LIMIT',
    'side=BUY',
    'quantity=1',
    'price=100.5',
    'timeInForce=IOC',
    'newClientOrderId=client-order',
    'newOrderRespType=RESULT',
    'reduceOnly=false',
    'positionSide=BOTH',
    'asterChain=Mainnet',
    `user=${ownerAddress}`,
    `signer=${agentAddress}`,
    'nonce=1700000000123456',
  ].join('&')
  return {
    id: 'aster-request-1',
    client_order_id: 'client-order',
    venue: 'aster',
    action: 'open',
    account: ownerAddress,
    signer: agentAddress,
    symbol: 'BTCUSDT',
    side: 'buy',
    amount: 1,
    price: 100.5,
    reduce_only: false,
    unsigned_payload: {
      domain: {
        name: 'AsterSignTransaction', version: '1', chainId: 1666,
        verifyingContract: '0x0000000000000000000000000000000000000000',
      },
      types: {
        EIP712Domain: [
          { name: 'name', type: 'string' },
          { name: 'version', type: 'string' },
          { name: 'chainId', type: 'uint256' },
          { name: 'verifyingContract', type: 'address' },
        ],
        Message: [{ name: 'msg', type: 'string' }],
      },
      primaryType: 'Message',
      message: { msg: message },
    },
    expires_at: '2099-08-10T12:00:00.000Z',
    created_at: '2026-08-10T12:00:00.000Z',
  }
}

function asterPrivateSigningRequest(action: SigningRequest['action']): SigningRequest {
  const request = asterSigningRequest()
  const routes: Partial<Record<SigningRequest['action'], { method: string; path: string }>> = {
    get_position_mode: { method: 'GET', path: '/fapi/v3/positionSide/dual' },
    get_account: { method: 'GET', path: '/fapi/v3/accountWithJoinMargin' },
    get_positions: { method: 'GET', path: '/fapi/v3/positionRisk' },
    get_leverage_brackets: { method: 'GET', path: '/fapi/v3/leverageBracket' },
    query_order: { method: 'GET', path: '/fapi/v3/order' },
    update_leverage: { method: 'POST', path: '/fapi/v3/leverage' },
    start_user_stream: { method: 'POST', path: '/fapi/v3/listenKey' },
    keepalive_user_stream: { method: 'PUT', path: '/fapi/v3/listenKey' },
    close_user_stream: { method: 'DELETE', path: '/fapi/v3/listenKey' },
  }
  let symbol = ''
  let clientOrderID = ''
  let leverage: number | undefined
  let operationQuery = ''
  if (action === 'get_positions' || action === 'get_leverage_brackets') {
    symbol = 'BTCUSDT'
    operationQuery = `symbol=${symbol}&`
  } else if (action === 'query_order') {
    symbol = 'BTCUSDT'
    clientOrderID = 'client-order'
    operationQuery = `symbol=${symbol}&origClientOrderId=${clientOrderID}&`
  } else if (action === 'update_leverage') {
    symbol = 'BTCUSDT'
    leverage = 5
    operationQuery = `symbol=${symbol}&leverage=${leverage}&`
  }
  return {
    ...request,
    id: `aster-${action}-1`,
    client_order_id: clientOrderID,
    action,
    symbol,
    side: '',
    amount: 0,
    price: 0,
    reduce_only: false,
    leverage,
    unsigned_payload: {
      ...(request.unsigned_payload as object),
      message: { msg: operationQuery + privateAuthQuery() },
    },
    venue_metadata: routes[action],
  }
}

function privateAuthQuery(): string {
  return `asterChain=Mainnet&user=${ownerAddress}&signer=${agentAddress}&nonce=1786363200000000`
}

class TestStorage implements StorageLike {
  readonly values = new Map<string, string>()
  getItem(key: string) { return this.values.get(key) ?? null }
  setItem(key: string, value: string) { this.values.set(key, value) }
  removeItem(key: string) { this.values.delete(key) }
}
