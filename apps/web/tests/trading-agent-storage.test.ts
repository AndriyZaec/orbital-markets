import assert from 'node:assert/strict'
import test from 'node:test'
import { webcrypto } from 'node:crypto'
import bs58 from 'bs58'
import { IDBFactory } from 'fake-indexeddb'
import nacl from 'tweetnacl'
import { privateKeyToAccount } from 'viem/accounts'

import { createTradingAgentStore, storageKey } from '../src/agents/storage.ts'
import type { StoredTradingAgent } from '../src/agents/types.ts'

const cryptography = webcrypto as unknown as Crypto
const databaseName = 'orbital-trading-agent-vault'

const hyperliquidAgent: StoredTradingAgent = {
  version: 2,
  venue: 'hyperliquid',
  ownerAddress: '0xAABBccDDeeFF0011223344556677889900AaBbCc',
  agentAddress: '0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A',
  privateKey: '0x1111111111111111111111111111111111111111111111111111111111111111',
  authorizedAt: '2026-08-10T12:00:00.000Z',
}

const asterAgent: StoredTradingAgent = {
  ...hyperliquidAgent,
  venue: 'aster',
  expiresAt: '2099-08-10T12:00:00.000Z',
}

const replacementPrivateKey = '0x2222222222222222222222222222222222222222222222222222222222222222'
const replacementAsterAgent: StoredTradingAgent = {
  ...asterAgent,
  agentAddress: privateKeyToAccount(replacementPrivateKey).address,
  privateKey: replacementPrivateKey,
}

test('encrypted agents survive reopening without persisting plaintext keys', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  await store.save(hyperliquidAgent)

  const key = storageKey('hyperliquid', hyperliquidAgent.ownerAddress)
  const envelope = await readVaultValue(indexedDB, 'agents', key) as Record<string, unknown>
  assert.equal(JSON.stringify(envelope).includes(hyperliquidAgent.privateKey), false)
  assert.equal(envelope.version, 2)
  assert.equal(envelope.keyAlgorithm, 'secp256k1')
  assert.ok(envelope.ciphertext instanceof Uint8Array)
  assert.ok(envelope.verification instanceof Uint8Array)

  const reopened = createTradingAgentStore(indexedDB, cryptography)
  assert.deepEqual(
    await reopened.loadForSigning('hyperliquid', hyperliquidAgent.ownerAddress.toUpperCase()),
    { ...hyperliquidAgent, ownerAddress: hyperliquidAgent.ownerAddress.toLowerCase() },
  )
})

test('pending Aster agent survives reload until approval is reconciled', async () => {
  const indexedDB = new IDBFactory()
  await createTradingAgentStore(indexedDB, cryptography).savePending(asterAgent)

  const reopened = createTradingAgentStore(indexedDB, cryptography)
  assert.deepEqual(
    await reopened.loadPendingForSigning('aster', asterAgent.ownerAddress),
    { ...asterAgent, ownerAddress: asterAgent.ownerAddress.toLowerCase() },
  )
  assert.equal(await reopened.loadForSigning('aster', asterAgent.ownerAddress), null)

  await reopened.promotePending(asterAgent)
  assert.equal(await reopened.loadPendingForSigning('aster', asterAgent.ownerAddress), null)
  assert.deepEqual(
    await reopened.loadForSigning('aster', asterAgent.ownerAddress),
    { ...asterAgent, ownerAddress: asterAgent.ownerAddress.toLowerCase() },
  )
})

test('concurrent Aster authorization cannot overwrite or clear an existing pending key', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  await store.savePending(asterAgent)
  await assert.rejects(store.savePending(replacementAsterAgent), /already exists/)

  await assert.rejects(store.promotePending(replacementAsterAgent), /aborted/)
  await store.clearPending('aster', asterAgent.ownerAddress, replacementAsterAgent.agentAddress)

  assert.equal(await store.loadForSigning('aster', asterAgent.ownerAddress), null)
  assert.equal(
    (await store.loadPendingForSigning('aster', asterAgent.ownerAddress))?.agentAddress,
    asterAgent.agentAddress,
  )
})

test('the browser profile master key is non-extractable and reused', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  await store.save(hyperliquidAgent)
  const firstKey = await readVaultValue(indexedDB, 'keys', 'aes-256-gcm') as CryptoKey

  assert.equal(firstKey.algorithm.name, 'AES-GCM')
  assert.equal((firstKey.algorithm as AesKeyAlgorithm).length, 256)
  assert.equal(firstKey.extractable, false)
  await assert.rejects(cryptography.subtle.exportKey('raw', firstKey))

  const reopened = createTradingAgentStore(indexedDB, cryptography)
  await reopened.save(asterAgent)
  const secondKey = await readVaultValue(indexedDB, 'keys', 'aes-256-gcm') as CryptoKey
  assert.equal(secondKey.extractable, false)
  assert.equal((await reopened.loadForSigning('hyperliquid', hyperliquidAgent.ownerAddress))?.agentAddress, hyperliquidAgent.agentAddress)
})

test('an invalid persisted master key fails closed', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  await store.ready()
  await writeVaultValue(indexedDB, 'keys', 'aes-256-gcm', { extractable: false })

  await assert.rejects(
    createTradingAgentStore(indexedDB, cryptography).ready(),
    /vault key is invalid/,
  )
})

test('each encrypted write uses fresh independent 12-byte IVs', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  const key = storageKey('hyperliquid', hyperliquidAgent.ownerAddress)
  await store.save(hyperliquidAgent)
  const first = await readVaultValue(indexedDB, 'agents', key) as { iv: Uint8Array; verificationIv: Uint8Array }
  await store.save(hyperliquidAgent)
  const second = await readVaultValue(indexedDB, 'agents', key) as { iv: Uint8Array; verificationIv: Uint8Array }

  assert.equal(first.iv.length, 12)
  assert.equal(second.iv.length, 12)
  assert.equal(first.verificationIv.length, 12)
  assert.equal(second.verificationIv.length, 12)
  assert.notDeepEqual(first.iv, first.verificationIv)
  assert.notDeepEqual(first.iv, second.iv)
  assert.notDeepEqual(first.verificationIv, second.verificationIv)
})

test('transient private-key decrypt failures preserve the envelope', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  const key = storageKey('hyperliquid', hyperliquidAgent.ownerAddress)
  await store.save(hyperliquidAgent)
  assert.equal(
    (await store.restore('hyperliquid', hyperliquidAgent.ownerAddress))?.agentAddress,
    hyperliquidAgent.agentAddress,
  )
  const failingStore = createTradingAgentStore(indexedDB, cryptoWithDecryptFailure())
  await assert.rejects(
    failingStore.loadForSigning('hyperliquid', hyperliquidAgent.ownerAddress),
    /WebCrypto temporarily unavailable/,
  )
  assert.notEqual(await readVaultValue(indexedDB, 'agents', key), undefined)
})

test('tampered authenticated metadata fails closed and deletes the envelope', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  const key = storageKey('hyperliquid', hyperliquidAgent.ownerAddress)
  await store.save(hyperliquidAgent)
  const envelope = await readVaultValue(indexedDB, 'agents', key) as Record<string, unknown>
  envelope.agentAddress = '0x0000000000000000000000000000000000000001'
  await writeVaultValue(indexedDB, 'agents', key, envelope)

  assert.equal(await store.restore('hyperliquid', hyperliquidAgent.ownerAddress), null)
  assert.equal(await readVaultValue(indexedDB, 'agents', key), undefined)
})

test('tampered private-key ciphertext fails closed during metadata restore', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  const key = storageKey('hyperliquid', hyperliquidAgent.ownerAddress)
  await store.save(hyperliquidAgent)
  const envelope = await readVaultValue(indexedDB, 'agents', key) as { ciphertext: Uint8Array }
  envelope.ciphertext[0] ^= 1
  await writeVaultValue(indexedDB, 'agents', key, envelope)

  assert.equal(await store.restore('hyperliquid', hyperliquidAgent.ownerAddress), null)
  assert.equal(await readVaultValue(indexedDB, 'agents', key), undefined)
})

test('tampered private-key IV fails closed during metadata restore', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  const key = storageKey('hyperliquid', hyperliquidAgent.ownerAddress)
  await store.save(hyperliquidAgent)
  const envelope = await readVaultValue(indexedDB, 'agents', key) as { iv: Uint8Array }
  envelope.iv[0] ^= 1
  await writeVaultValue(indexedDB, 'agents', key, envelope)

  assert.equal(await store.restore('hyperliquid', hyperliquidAgent.ownerAddress), null)
  assert.equal(await readVaultValue(indexedDB, 'agents', key), undefined)
})

test('malformed and expired records fail closed', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  const malformedKey = storageKey('pacifica', 'Owner111')
  await writeVaultValue(indexedDB, 'agents', malformedKey, { version: 2, venue: 'pacifica' })

  assert.equal(await store.restore('pacifica', 'Owner111'), null)
  await assert.rejects(
    store.save({ ...asterAgent, expiresAt: '2020-01-01T00:00:00.000Z' }),
    /Trading agent is invalid/,
  )
})

test('an owner mismatch does not expose or delete another owner agent', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  await store.save(hyperliquidAgent)

  assert.equal(
    await store.restore('hyperliquid', '0x0000000000000000000000000000000000000001'),
    null,
  )
  assert.equal(
    (await store.restore('hyperliquid', hyperliquidAgent.ownerAddress))?.agentAddress,
    hyperliquidAgent.agentAddress,
  )
})

test('Pacifica key material derives the authenticated public key', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  const keyPair = nacl.sign.keyPair.fromSeed(Uint8Array.from({ length: 32 }, (_, index) => index + 32))
  const agent: StoredTradingAgent = {
    version: 2,
    venue: 'pacifica',
    ownerAddress: 'FAe4sisG95oZ42w7buUn5qEE4TAnfTTFPiguZUHmhiF',
    agentAddress: bs58.encode(keyPair.publicKey),
    privateKey: bs58.encode(keyPair.secretKey),
    authorizedAt: '2026-08-10T12:00:00.000Z',
  }
  await store.save(agent)

  assert.deepEqual(await store.loadForSigning('pacifica', agent.ownerAddress), agent)
  await assert.rejects(store.save({ ...agent, agentAddress: 'Agent111' }), /Trading agent is invalid/)
})

test('clear removes only the selected owner and venue envelope', async () => {
  const indexedDB = new IDBFactory()
  const store = createTradingAgentStore(indexedDB, cryptography)
  await store.save(hyperliquidAgent)
  await store.save(asterAgent)

  await store.clear('hyperliquid', hyperliquidAgent.ownerAddress)

  assert.equal(await store.restore('hyperliquid', hyperliquidAgent.ownerAddress), null)
  assert.equal((await store.restore('aster', asterAgent.ownerAddress))?.agentAddress, asterAgent.agentAddress)
})

function cryptoWithDecryptFailure(): Crypto {
  const subtle = new Proxy(cryptography.subtle, {
    get(target, property) {
      if (property === 'decrypt') {
        return async () => { throw new Error('WebCrypto temporarily unavailable') }
      }
      const value = Reflect.get(target, property, target) as unknown
      return typeof value === 'function' ? value.bind(target) : value
    },
  })
  return {
    subtle,
    getRandomValues: cryptography.getRandomValues.bind(cryptography),
    randomUUID: cryptography.randomUUID.bind(cryptography),
  }
}

async function readVaultValue(indexedDB: IDBFactory, storeName: string, key: IDBValidKey): Promise<unknown> {
  const database = await openVault(indexedDB)
  return new Promise((resolve, reject) => {
    const request = database.transaction(storeName).objectStore(storeName).get(key)
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

async function writeVaultValue(
  indexedDB: IDBFactory,
  storeName: string,
  key: IDBValidKey,
  value: unknown,
): Promise<void> {
  const database = await openVault(indexedDB)
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(storeName, 'readwrite')
    transaction.objectStore(storeName).put(value, key)
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error)
  })
}

function openVault(indexedDB: IDBFactory): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, 1)
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}
