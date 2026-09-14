import assert from 'node:assert/strict'
import test from 'node:test'
import { privateKeyToAccount } from 'viem/accounts'

import {
  authorizeAsterAgent,
  buildAsterApproveAgentAction,
  buildAsterApproveAgentTypedData,
  buildAsterApproveBuilderTypedData,
  signAsterAgentRequest,
} from '../src/agents/aster-agent.ts'
import { signWithStoredTradingAgent } from '../src/agents/signing.ts'
import type { StoredTradingAgent } from '../src/agents/types.ts'
import type { SigningRequest } from '../src/types/signing.ts'
import { TestTradingAgentStore } from './trading-agent-test-store.ts'
import builderConfig from '../../api/internal/venue/hyperliquid/live/builder_config.json' with { type: 'json' }

const ownerAddress = '0x14791697260E4c9A71f18484C9f997B308e59325'
const ownerAccount = privateKeyToAccount('0x0123456789012345678901234567890123456789012345678901234567890123')
const privateKey = '0x1111111111111111111111111111111111111111111111111111111111111111'
const agentAddress = '0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A'
const dataAgentAddress = '0x3333333333333333333333333333333333333333'

test('Aster management approvals use the MetaMask-compatible BSC domain', async () => {
  const action = buildAsterApproveAgentAction(ownerAddress, agentAddress, 1_748_970_123_456)
  const agentTypedData = buildAsterApproveAgentTypedData(action)
  const builderTypedData = buildAsterApproveBuilderTypedData(action)

  assert.equal(action.nonce, 1_748_970_123_456_000)
  assert.equal(action.canSpotTrade, false)
  assert.equal(action.canPerpTrade, true)
  assert.equal(action.canWithdraw, false)
  assert.equal(action.ipWhitelist, '')
  assert.equal(action.signatureChainId, 56)
  assert.equal(agentTypedData.domain.chainId, 56n)
  assert.equal(agentTypedData.primaryType, 'ApproveAgent')
  assert.deepEqual(Object.keys(agentTypedData.message), [
    'AgentName', 'AgentAddress', 'IpWhitelist', 'Expired', 'CanSpotTrade',
    'CanPerpTrade', 'CanWithdraw', 'AsterChain', 'User', 'Nonce',
  ])
  assert.equal(builderTypedData.domain.chainId, 56n)
  assert.equal(builderTypedData.primaryType, 'ApproveBuilder')
  assert.deepEqual(Object.keys(builderTypedData.message), [
    'Builder', 'MaxFeeRate', 'BuilderName', 'AsterChain', 'User', 'Nonce',
  ])
  assert.match(await ownerAccount.signTypedData(agentTypedData), /^0x[0-9a-f]{130}$/)
  assert.match(await ownerAccount.signTypedData(builderTypedData), /^0x[0-9a-f]{130}$/)
})

test('Aster authorization relays no private key and persists only after acceptance', async () => {
  const storage = new TestTradingAgentStore()
  let relayed = ''
  const signatures: string[] = []
  const submissions: string[] = []
  const now = Date.now()
  const agent = await authorizeAsterAgent({
    storage,
    ownerAddress,
    now: () => now,
    signTypedData: async (typedData) => {
      signatures.push(typedData.primaryType === 'ApproveBuilder'
        ? 'builder'
        : typedData.message.CanPerpTrade ? 'execution' : 'read-only')
      return ownerAccount.signTypedData(typedData)
    },
    prepareReadOnly: async (executionAgent) => readOnlyPreparation(executionAgent, now),
    authorizeReadOnly: async () => {
      submissions.push('read-only')
      assert.equal(storage.values.size, 0)
    },
    relay: async (request) => {
      submissions.push('execution')
      relayed = JSON.stringify(request)
      assert.equal(storage.values.size, 0)
    },
  })

  assert.deepEqual(signatures, ['builder', 'read-only', 'execution'])
  assert.deepEqual(submissions, ['read-only', 'execution'])
  assert.equal(relayed.includes(agent.privateKey), false)
  assert.equal(relayed.includes('private'), false)
  assert.equal((await storage.restore('aster', ownerAddress))?.agentAddress, agent.agentAddress)
})

test('Aster authorization does not relay after the owner changes', async () => {
  let relayed = false
  await assert.rejects(authorizeAsterAgent({
    storage: new TestTradingAgentStore(),
    ownerAddress,
    signTypedData: (typedData) => ownerAccount.signTypedData(typedData),
    prepareReadOnly: async (executionAgent) => readOnlyPreparation(executionAgent, Date.now()),
    authorizeReadOnly: async () => { relayed = true },
    ownerStillCurrent: () => false,
    relay: async () => { relayed = true },
  }), /owner changed/)
  assert.equal(relayed, false)
})

test('Aster authorization keeps the previous browser agent when execution approval fails', async () => {
  const storage = new TestTradingAgentStore()
  const previous = asterAgent()
  await storage.save(previous)
  let readOnlyAccepted = false

  await assert.rejects(authorizeAsterAgent({
    storage,
    ownerAddress,
    signTypedData: (typedData) => ownerAccount.signTypedData(typedData),
    prepareReadOnly: async (executionAgent) => readOnlyPreparation(executionAgent, Date.now()),
    authorizeReadOnly: async () => { readOnlyAccepted = true },
    relay: async () => { throw new Error('execution approval failed') },
  }), /execution approval failed/)

  assert.equal(readOnlyAccepted, true)
  assert.equal((await storage.restore('aster', ownerAddress))?.agentAddress, previous.agentAddress)
})

function readOnlyPreparation(executionAgent: string, now: number) {
  assert.match(executionAgent, /^0x[0-9a-fA-F]{40}$/)
  return {
    probe_id: 'authorization-1',
    approval: {
      user: ownerAddress,
      nonce: now * 1000,
      agentName: 'OrbitalData' as const,
      agentAddress: dataAgentAddress,
      ipWhitelist: '' as const,
      expired: now + 365 * 24 * 60 * 60 * 1000,
      canSpotTrade: false as const,
      canPerpTrade: false as const,
      canWithdraw: false as const,
      asterChain: 'Mainnet' as const,
      signatureChainId: 56 as const,
    },
  }
}

test('a local Aster agent signs the exact allowed IOC payload', async () => {
  const storage = new TestTradingAgentStore()
  await storage.save(asterAgent())

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

test('Aster signing rejects altered builder attribution', async () => {
  const request = asterSigningRequest()
  const payload = request.unsigned_payload as { message: { msg: string } }
  payload.message.msg = payload.message.msg.replace(builderConfig.address, '0x3333333333333333333333333333333333333333')
  await assert.rejects(signAsterAgentRequest(request, asterAgent()), /not an allowed IOC order/)

  const alteredFee = asterSigningRequest()
  const alteredFeePayload = alteredFee.unsigned_payload as { message: { msg: string } }
  alteredFeePayload.message.msg = alteredFeePayload.message.msg.replace('feeRate=0.0002', 'feeRate=0.001')
  await assert.rejects(signAsterAgentRequest(alteredFee, asterAgent()), /not an allowed IOC order/)
})

test('Aster signing rejects an agent authorized before builder attribution', async () => {
  const legacyAgent = asterAgent()
  delete legacyAgent.builderAddress
  await assert.rejects(signAsterAgentRequest(asterSigningRequest(), legacyAgent), /not an allowed IOC order/)
})

test('Aster agent signs browser leverage updates', async () => {
  const signed = await signAsterAgentRequest(asterLeverageSigningRequest(), asterAgent())
  assert.match(signed.signature, /^0x[0-9a-f]{130}$/)
})

test('Aster agent rejects leverage request fields outside the operation policy', async () => {
  const request = asterLeverageSigningRequest()
  const payload = request.unsigned_payload as { message: { msg: string } }
  payload.message.msg = `withdraw=true&${payload.message.msg}`
  await assert.rejects(signAsterAgentRequest(request, asterAgent()), /not an allowed private request/)
})

test('Aster agent rejects obsolete browser read requests', async () => {
  const request = {
    ...asterLeverageSigningRequest(),
    action: 'get_account',
    symbol: '',
    leverage: undefined,
  } as unknown as SigningRequest
  await assert.rejects(signAsterAgentRequest(request, asterAgent()), /not an allowed private request/)
})

function asterAgent(): StoredTradingAgent {
  assert.equal(privateKeyToAccount(privateKey).address.toLowerCase(), agentAddress.toLowerCase())
  return {
    version: 2,
    venue: 'aster',
    ownerAddress,
    agentAddress,
    privateKey,
    authorizedAt: '2026-08-10T12:00:00.000Z',
    expiresAt: '2099-08-10T12:00:00.000Z',
    builderAddress: builderConfig.address,
  }
}

function asterSigningRequest(): SigningRequest {
  const message = [
    'symbol=BTCUSDT',
    'type=LIMIT',
    `builder=${builderConfig.address}`,
    `feeRate=${String(builderConfig.fee / 100_000)}`,
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

function asterLeverageSigningRequest(): SigningRequest {
  const request = asterSigningRequest()
  return {
    ...request,
    id: 'aster-update-leverage-1',
    client_order_id: '',
    action: 'update_leverage',
    symbol: 'BTCUSDT',
    side: '',
    amount: 0,
    price: 0,
    reduce_only: false,
    leverage: 5,
    unsigned_payload: {
      ...(request.unsigned_payload as object),
      message: { msg: `symbol=BTCUSDT&leverage=5&${privateAuthQuery()}` },
    },
    venue_metadata: { method: 'POST', path: '/fapi/v3/leverage' },
  }
}

function privateAuthQuery(): string {
  return `asterChain=Mainnet&user=${ownerAddress}&signer=${agentAddress}&nonce=1786363200000000`
}
