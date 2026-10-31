import assert from 'node:assert/strict'
import test from 'node:test'
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { Keypair, PublicKey, Transaction, TransactionInstruction, SystemProgram } from '@solana/web3.js'
import bs58 from 'bs58'
import nacl from 'tweetnacl'
import { authorizePhoenixAgent, restorePhoenixAgent, revokePhoenixAgent, PhoenixPendingError, PhoenixReplacementRequired, type PhoenixTransport, type PhoenixResult } from '../src/agents/phoenix-agent.ts'
import { phoenixProgram, phoenixLogAuthority, phoenixGlobalConfiguration, phoenixTrader, validatePhoenixDelegation, signPhoenixDelegation, type PhoenixAction } from '../src/agents/phoenix-message.ts'
import type { SigningRequest } from '../src/types/signing.ts'
import { TestTradingAgentStore } from './trading-agent-test-store.ts'

const owner = Keypair.fromSeed(new Uint8Array(32).fill(1))
const payer = Keypair.fromSeed(new Uint8Array(32).fill(3))
const third = Keypair.fromSeed(new Uint8Array(32).fill(4)).publicKey.toBase58()
const ownerAddress = owner.publicKey.toBase58()

function signingRequest(agent: string, action: PhoenixAction, id = 'request-1'): SigningRequest {
  const target = action === 'revoke' ? ownerAddress : agent
  const trader = phoenixTrader(ownerAddress)
  const instruction = new TransactionInstruction({
    programId: new PublicKey(phoenixProgram),
    keys: [phoenixProgram, phoenixLogAuthority, phoenixGlobalConfiguration, ownerAddress, trader, target].map((key, index) => ({ pubkey: new PublicKey(key), isSigner: index === 3, isWritable: index === 4 })),
    data: createHash('sha256').update('global:delegate_trader').digest().subarray(0, 8),
  })
  const tx = new Transaction({ feePayer: payer.publicKey, recentBlockhash: bs58.encode(new Uint8Array(32).fill(9)) }).add(instruction)
  const message = tx.serializeMessage()
  const message_base64 = message.toString('base64')
  return {
    id, client_order_id: id, venue: 'phoenix', action, account: ownerAddress, signer: ownerAddress,
    symbol: '', side: '', amount: 0, price: 0, reduce_only: false, created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 30_000).toISOString(),
    unsigned_payload: { message_base64, action, owner_authority: ownerAddress, new_position_authority: target, fee_payer: payer.publicKey.toBase58(), trader_account: trader },
    venue_metadata: { message_base64, message_sha256: createHash('sha256').update(message).digest('hex'), recent_blockhash: tx.recentBlockhash, last_valid_block_height: 150, trader_account: trader, trader_pda_index: 0, subaccount_index: 0,
      required_signers: [{ index: 0, pubkey: payer.publicKey.toBase58(), role: 'fee_payer' }, { index: 1, pubkey: ownerAddress, role: 'owner_authority' }] },
  }
}

function fixture() {
  const storage = new TestTradingAgentStore()
  const rows = new Map<string, PhoenixResult>()
  let nextID = 0, sends = 0, signatures = 0, current = true
  let autoConfirm = true, loseResponse = false, leavePrepared = false
  let currentAuthority = ownerAddress, registered = ''
  let signHook: (() => Promise<void>) | undefined
  const transport: PhoenixTransport = {
    prepare: async (account, agent, action, replace, expectedAuthority) => {
      if (action === 'delegate' && currentAuthority !== account && currentAuthority !== agent && (!replace || expectedAuthority !== currentAuthority)) throw new PhoenixReplacementRequired(currentAuthority)
      const request = signingRequest(agent, action, `request-${++nextID}`)
      const result: PhoenixResult = { request_id: request.id, owner: account, action, target_authority: action === 'revoke' ? account : agent, state: 'prepared', signing_request: request }
      rows.set(result.request_id, result)
      return structuredClone(result)
    },
    submit: async (account, signed) => {
      sends++
      const row = rows.get(signed.request_id)!
      assert.equal(account, row.owner)
      assert.equal(signed.signer_address, ownerAddress)
      const pending = await storage.loadPendingForSigning('phoenix', account)
      const active = await storage.loadForSigning('phoenix', account)
      assert.equal((pending ?? active)?.phoenixOperation?.submissionStarted, true)
      if (!leavePrepared) row.state = 'pending'
      if (loseResponse) throw new TypeError('Failed to fetch')
      return structuredClone(row)
    },
    reconcile: async (account, id) => {
      const row = rows.get(id)!
      assert.equal(account, row.owner)
      if (row.state === 'pending' && autoConfirm) {
        row.state = 'confirmed'
        currentAuthority = row.target_authority
        registered = row.action === 'delegate' ? row.target_authority : ''
      }
      return structuredClone(row)
    },
    authority: async (account, agent) => ({ owner: account, agent, position_authority: currentAuthority, authorized: registered === agent && currentAuthority === agent }),
  }
  const options = {
    storage, ownerAddress, transport, isCurrent: () => current, wait: async () => {},
    signTransaction: async (transaction: Transaction) => {
      signatures++
      const pending = await storage.loadPendingForSigning('phoenix', ownerAddress)
      const active = await storage.loadForSigning('phoenix', ownerAddress)
      assert.ok((pending ?? active)?.phoenixOperation?.requestID, 'operation must be saved before wallet prompt')
      await signHook?.()
      transaction.partialSign(owner)
      return transaction
    },
  }
  return { options, storage, rows, counts: () => ({ sends, signatures, preparations: nextID }), setCurrent: (value: boolean) => { current = value }, setConfirm: (value: boolean) => { autoConfirm = value }, dropResponse: () => { loseResponse = true }, leavePrepared: () => { leavePrepared = true }, setAuthority: (value: string) => { currentAuthority = value }, onSign: (hook: () => Promise<void>) => { signHook = hook } }
}

test('Phoenix saves pending key before signing and only promotes confirmed authority', async () => {
  const f = fixture()
  const agent = await authorizePhoenixAgent(f.options)
  assert.equal(agent.venue, 'phoenix')
  assert.equal(await f.storage.loadPendingForSigning('phoenix', ownerAddress), null)
  assert.equal((await f.storage.loadForSigning('phoenix', ownerAddress))?.privateKey, agent.privateKey)
  assert.deepEqual(f.counts(), { sends: 1, signatures: 1, preparations: 1 })
  assert.equal((await restorePhoenixAgent(f.options))?.agentAddress, agent.agentAddress)
})

test('lost submit response retains pending key and reload reconciles without another send', async () => {
  const f = fixture(); f.setConfirm(false); f.dropResponse()
  await assert.rejects(authorizePhoenixAgent(f.options), PhoenixPendingError)
  const pending = await f.storage.loadPendingForSigning('phoenix', ownerAddress)
  assert.ok(pending?.privateKey)
  assert.equal(pending.phoenixOperation?.submissionStarted, true)
  assert.equal(await f.storage.loadForSigning('phoenix', ownerAddress), null)
  f.setConfirm(true)
  const restored = await restorePhoenixAgent(f.options)
  assert.equal(restored?.agentAddress, pending.agentAddress)
  assert.equal((await f.storage.loadForSigning('phoenix', ownerAddress))?.privateKey, pending.privateKey)
  assert.deepEqual(f.counts(), { sends: 1, signatures: 1, preparations: 1 })
})

test('a prepared read after an uncertain submit never signs or sends again', async () => {
  const f = fixture(); f.leavePrepared(); f.dropResponse()
  await assert.rejects(authorizePhoenixAgent(f.options), PhoenixPendingError)
  await assert.rejects(authorizePhoenixAgent(f.options), PhoenixPendingError)
  assert.deepEqual(f.counts(), { sends: 1, signatures: 1, preparations: 1 })
  f.rows.get('request-1')!.state = 'expired'
  await assert.rejects(authorizePhoenixAgent(f.options), PhoenixPendingError)
  assert.equal(f.counts().preparations, 2)
})

test('owner change during wallet signing preserves the key and prevents relay', async () => {
  const f = fixture(); f.onSign(async () => f.setCurrent(false))
  await assert.rejects(authorizePhoenixAgent(f.options), /owner changed/)
  assert.equal(f.counts().sends, 0)
  assert.ok((await f.storage.loadPendingForSigning('phoenix', ownerAddress))?.privateKey)
})

test('vault promotion failure preserves the only delegated key for reload recovery', async () => {
  const f = fixture()
  const promote = f.storage.promotePending.bind(f.storage)
  f.storage.promotePending = async () => { throw new Error('disk unavailable') }
  await assert.rejects(authorizePhoenixAgent(f.options), /disk unavailable/)
  assert.ok(await f.storage.loadPendingForSigning('phoenix', ownerAddress))
  f.storage.promotePending = promote
  assert.ok(await restorePhoenixAgent(f.options))
  assert.equal(f.counts().sends, 1)
})

test('revoke retains the active key until confirmed owner reset, including reload', async () => {
  const f = fixture(); const agent = await authorizePhoenixAgent(f.options)
  f.setConfirm(false)
  await assert.rejects(revokePhoenixAgent(f.options), PhoenixPendingError)
  assert.equal((await f.storage.loadForSigning('phoenix', ownerAddress))?.privateKey, agent.privateKey)
  f.setConfirm(true)
  assert.equal(await restorePhoenixAgent(f.options), null)
  assert.equal(await f.storage.loadForSigning('phoenix', ownerAddress), null)
  assert.equal(f.counts().sends, 2)
})

test('confirmed revoke clears both a previous active key and a pending replacement', async () => {
  const f = fixture(); const first = await authorizePhoenixAgent(f.options)
  const promote = f.storage.promotePending.bind(f.storage)
  f.storage.promotePending = async () => { throw new Error('disk unavailable') }
  await assert.rejects(authorizePhoenixAgent({ ...f.options, replaceExistingDelegate: true, expectedAuthority: first.agentAddress }), /disk unavailable/)
  const pending = await f.storage.loadPendingForSigning('phoenix', ownerAddress)
  assert.notEqual(pending?.agentAddress, first.agentAddress)
  f.storage.promotePending = promote
  await revokePhoenixAgent(f.options)
  assert.equal(await f.storage.loadForSigning('phoenix', ownerAddress), null)
  assert.equal(await f.storage.loadPendingForSigning('phoenix', ownerAddress), null)
})

test('third-party delegate replacement requires explicit confirmation and reuses pending key', async () => {
  const f = fixture(); f.setAuthority(third)
  await assert.rejects(authorizePhoenixAgent(f.options), PhoenixReplacementRequired)
  const pending = await f.storage.loadPendingForSigning('phoenix', ownerAddress)
  assert.equal(f.counts().signatures, 0)
  const authorized = await authorizePhoenixAgent({ ...f.options, replaceExistingDelegate: true, expectedAuthority: third })
  assert.equal(authorized.privateKey, pending?.privateKey)
})

test('restore refuses externally replaced authority without deleting a recoverable key', async () => {
  const f = fixture(); const agent = await authorizePhoenixAgent(f.options); f.setAuthority(third)
  await assert.rejects(restorePhoenixAgent(f.options), /authority no longer matches/)
  assert.equal((await f.storage.loadForSigning('phoenix', ownerAddress))?.privateKey, agent.privateKey)
})

test('owner signing validates actual instructions and rejects wallet mutations', async () => {
  const agent = bs58.encode(nacl.sign.keyPair().publicKey)
  const request = signingRequest(agent, 'delegate')
  const expected = { owner: ownerAddress, agent, action: 'delegate' as const }
  assert.ok(await validatePhoenixDelegation(request, expected))
  const wrong = signingRequest(third, 'delegate')
  await assert.rejects(validatePhoenixDelegation(wrong, expected), /mismatch/)
  await assert.rejects(signPhoenixDelegation(request, { ...expected, isCurrent: () => true, signTransaction: async (tx) => {
    tx.add(SystemProgram.transfer({ fromPubkey: owner.publicKey, toPubkey: payer.publicKey, lamports: 1 }))
    tx.partialSign(owner); return tx
  } }), /Wallet changed/)
  const expired = { ...request, expires_at: new Date(0).toISOString() }
  await assert.rejects(validatePhoenixDelegation(expired, expected), /expiry/)
})

test('unavailable infrastructure creates no unused pending key or owner prompt', async () => {
  const f = fixture()
  f.options.transport.available = async () => { throw new Error('infrastructure unavailable') }
  await assert.rejects(authorizePhoenixAgent(f.options), /infrastructure unavailable/)
  assert.equal(await f.storage.loadPendingForSigning('phoenix', ownerAddress), null)
  assert.deepEqual(f.counts(), { sends: 0, signatures: 0, preparations: 0 })
})

test('failure to persist operation metadata prevents signing and preserves pending secret', async () => {
  const f = fixture()
  f.storage.updatePending = async () => { throw new Error('disk unavailable') }
  await assert.rejects(authorizePhoenixAgent(f.options), /disk unavailable/)
  assert.ok((await f.storage.loadPendingForSigning('phoenix', ownerAddress))?.privateKey)
  assert.equal(f.counts().signatures, 0)
  assert.equal(f.counts().sends, 0)
})

test('request ID mismatch fails before the owner wallet is called', async () => {
  const f = fixture()
  const prepare = f.options.transport.prepare
  f.options.transport.prepare = async (...args) => {
    const result = await prepare(...args)
    result.request_id = 'wrong-result-ID'
    return result
  }
  await assert.rejects(authorizePhoenixAgent(f.options), /correlation mismatch/)
  assert.equal(f.counts().signatures, 0)
})

test('browser validator accepts exact delegation/revoke bytes from the pinned Rust helper', async (t) => {
  const path = process.env.ORBITAL_PHOENIX_HELPER_TEST_PATH
  if (!path) { t.skip('set ORBITAL_PHOENIX_HELPER_TEST_PATH to built phoenix-helper'); return }
  const agent = bs58.encode(nacl.sign.keyPair().publicKey)
  for (const action of ['delegate', 'revoke'] as const) {
    const response = JSON.parse(execFileSync(path, { input: JSON.stringify({ protocol_version: 1, request_id: 'fixture', operation: 'build_delegation', payload: {
      action, owner_authority: ownerAddress, new_position_authority: action === 'revoke' ? ownerAddress : agent, fee_payer: payer.publicKey.toBase58(), recent_blockhash: bs58.encode(new Uint8Array(32).fill(9)), last_valid_block_height: 150,
    } }) + '\n', env: { ...process.env, PHOENIX_ENV: '' } }).toString())
    assert.equal(response.ok, true)
    const request = signingRequest(agent, action)
    request.venue_metadata = response.result
    request.unsigned_payload = { ...(request.unsigned_payload as object), message_base64: response.result.message_base64 }
    await validatePhoenixDelegation(request, { owner: ownerAddress, agent, action })
  }
})
