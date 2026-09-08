import bs58 from 'bs58'
import nacl from 'tweetnacl'
import { privateKeyToAccount } from 'viem/accounts'
import type { Hex } from 'viem'

import type { StoredTradingAgent, Venue } from './types'

const databaseName = 'orbital-trading-agent-vault'
const databaseVersion = 1
const agentStoreName = 'agents'
const keyStoreName = 'keys'
const masterKeyID = 'aes-256-gcm'
const verificationValue = 'orbital-agent-envelope-v2'

type KeyAlgorithm = 'ed25519' | 'secp256k1'

interface EncryptedTradingAgent {
  version: 2
  venue: Venue
  ownerAddress: string
  agentAddress: string
  keyAlgorithm: KeyAlgorithm
  authorizedAt: string
  expiresAt?: string
  builderAddress?: string
  builderCode?: string
  iv: Uint8Array<ArrayBuffer>
  ciphertext: Uint8Array<ArrayBuffer>
  verificationIv: Uint8Array<ArrayBuffer>
  verification: Uint8Array<ArrayBuffer>
}

export interface TradingAgentStore {
  ready(): Promise<void>
  save(agent: StoredTradingAgent): Promise<void>
  restore(venue: Venue, ownerAddress: string): Promise<Omit<StoredTradingAgent, 'privateKey'> | null>
  loadForSigning(venue: Venue, ownerAddress: string): Promise<StoredTradingAgent | null>
  clear(venue: Venue, ownerAddress: string): Promise<void>
}

export function createTradingAgentStore(
  indexedDB: IDBFactory,
  cryptography: Crypto,
): TradingAgentStore {
  return new IndexedDBTradingAgentStore(indexedDB, cryptography)
}

class IndexedDBTradingAgentStore implements TradingAgentStore {
  private readonly database: Promise<IDBDatabase>
  private readonly cryptography: Crypto
  private masterKey: Promise<CryptoKey> | null = null

  constructor(indexedDB: IDBFactory, cryptography: Crypto) {
    this.database = openDatabase(indexedDB)
    this.cryptography = cryptography
  }

  async ready(): Promise<void> {
    await this.getMasterKey()
  }

  async save(agent: StoredTradingAgent): Promise<void> {
    const normalized = normalizeAgent(agent)
    if (!validAgent(normalized) || !keyPairMatches(normalized)) {
      throw new Error('Trading agent is invalid')
    }
    const envelope = envelopeMetadata(normalized)
    const iv = randomIV(this.cryptography)
    const verificationIv = randomIV(this.cryptography, iv)
    const masterKey = await this.getMasterKey()
    const ciphertext = await this.cryptography.subtle.encrypt(
      { name: 'AES-GCM', iv, additionalData: associatedData(envelope) },
      masterKey,
      new TextEncoder().encode(normalized.privateKey),
    )
    const encryptedKey = new Uint8Array(ciphertext)
    const verification = await this.cryptography.subtle.encrypt(
      { name: 'AES-GCM', iv: verificationIv, additionalData: verificationData(envelope, iv, encryptedKey) },
      masterKey,
      new TextEncoder().encode(verificationValue),
    )
    const database = await this.database
    await writeValue(database, agentStoreName, storageKey(normalized.venue, normalized.ownerAddress), {
      ...envelope,
      iv,
      ciphertext: encryptedKey,
      verificationIv,
      verification: new Uint8Array(verification),
    } satisfies EncryptedTradingAgent)
  }

  async restore(venue: Venue, ownerAddress: string): Promise<Omit<StoredTradingAgent, 'privateKey'> | null> {
    const key = storageKey(venue, ownerAddress)
    const database = await this.database
    const value = await readValue(database, agentStoreName, key)
    if (!value) return null
    if (!validEnvelope(value, venue, ownerAddress)) {
      await deleteValue(database, agentStoreName, key)
      return null
    }
    let verified: ArrayBuffer
    try {
      verified = await this.cryptography.subtle.decrypt(
        {
          name: 'AES-GCM',
          iv: value.verificationIv,
          additionalData: verificationData(value, value.iv, value.ciphertext),
        },
        await this.getMasterKey(),
        value.verification,
      )
    } catch (error) {
      if (!authenticationFailed(error)) throw error
      await deleteValue(database, agentStoreName, key)
      return null
    }
    if (new TextDecoder().decode(verified) !== verificationValue) {
      await deleteValue(database, agentStoreName, key)
      return null
    }
    return {
      version: value.version,
      venue: value.venue,
      ownerAddress: value.ownerAddress,
      agentAddress: value.agentAddress,
      authorizedAt: value.authorizedAt,
      ...(value.expiresAt ? { expiresAt: value.expiresAt } : {}),
      ...(value.builderAddress ? { builderAddress: value.builderAddress } : {}),
      ...(value.builderCode ? { builderCode: value.builderCode } : {}),
    }
  }

  async loadForSigning(venue: Venue, ownerAddress: string): Promise<StoredTradingAgent | null> {
    const key = storageKey(venue, ownerAddress)
    const database = await this.database
    const value = await readValue(database, agentStoreName, key)
    if (!value) return null
    if (!validEnvelope(value, venue, ownerAddress)) {
      await deleteValue(database, agentStoreName, key)
      return null
    }

    let plaintext: ArrayBuffer
    try {
      plaintext = await this.cryptography.subtle.decrypt(
        { name: 'AES-GCM', iv: value.iv, additionalData: associatedData(value) },
        await this.getMasterKey(),
        value.ciphertext,
      )
    } catch (error) {
      if (!authenticationFailed(error)) throw error
      await deleteValue(database, agentStoreName, key)
      return null
    }
    const agent: StoredTradingAgent = {
      version: 2,
      venue: value.venue,
      ownerAddress: value.ownerAddress,
      agentAddress: value.agentAddress,
      privateKey: new TextDecoder().decode(plaintext),
      authorizedAt: value.authorizedAt,
      ...(value.expiresAt ? { expiresAt: value.expiresAt } : {}),
      ...(value.builderAddress ? { builderAddress: value.builderAddress } : {}),
      ...(value.builderCode ? { builderCode: value.builderCode } : {}),
    }
    if (keyPairMatches(agent)) return agent
    await deleteValue(database, agentStoreName, key)
    return null
  }

  async clear(venue: Venue, ownerAddress: string): Promise<void> {
    const database = await this.database
    await deleteValue(database, agentStoreName, storageKey(venue, ownerAddress))
  }

  private getMasterKey(): Promise<CryptoKey> {
    this.masterKey ??= this.loadOrCreateMasterKey()
    return this.masterKey
  }

  private async loadOrCreateMasterKey(): Promise<CryptoKey> {
    const database = await this.database
    const candidate = await this.cryptography.subtle.generateKey(
      { name: 'AES-GCM', length: 256 },
      false,
      ['encrypt', 'decrypt'],
    )
    return new Promise((resolve, reject) => {
      const transaction = database.transaction(keyStoreName, 'readwrite')
      const store = transaction.objectStore(keyStoreName)
      const request = store.get(masterKeyID)
      let selected: CryptoKey | null = null
      request.onsuccess = () => {
        if (request.result !== undefined) {
          if (!validMasterKey(request.result)) {
            transaction.abort()
            return
          }
          selected = request.result
          return
        }
        selected = candidate
        store.add(candidate, masterKeyID)
      }
      transaction.oncomplete = () => {
        if (selected) resolve(selected)
        else reject(new Error('Trading agent vault key is unavailable'))
      }
      transaction.onerror = () => reject(transaction.error ?? new Error('Unable to open trading agent vault key'))
      transaction.onabort = () => reject(transaction.error ?? new Error('Trading agent vault key is invalid'))
    })
  }
}

export function storageKey(venue: Venue, ownerAddress: string): string {
  return `${venue}:${normalizeOwner(venue, ownerAddress)}`
}

function normalizeAgent(agent: StoredTradingAgent): StoredTradingAgent {
  return { ...agent, ownerAddress: normalizeOwner(agent.venue, agent.ownerAddress) }
}

function normalizeOwner(venue: Venue, ownerAddress: string): string {
  return isEVMVenue(venue) ? ownerAddress.toLowerCase() : ownerAddress
}

function isEVMVenue(venue: Venue): boolean {
  return venue === 'hyperliquid' || venue === 'aster'
}

function keyAlgorithm(venue: Venue): KeyAlgorithm {
  return venue === 'pacifica' ? 'ed25519' : 'secp256k1'
}

function keyPairMatches(agent: StoredTradingAgent): boolean {
  try {
    if (isEVMVenue(agent.venue)) {
      if (!/^0x[0-9a-fA-F]{64}$/.test(agent.privateKey)) return false
      return privateKeyToAccount(agent.privateKey as Hex).address.toLowerCase() === agent.agentAddress.toLowerCase()
    }
    const secretKey = bs58.decode(agent.privateKey)
    if (secretKey.length !== nacl.sign.secretKeyLength) return false
    return bs58.encode(nacl.sign.keyPair.fromSecretKey(secretKey).publicKey) === agent.agentAddress
  } catch {
    return false
  }
}

function validAgent(agent: StoredTradingAgent): boolean {
  return agent.version === 2 && validMetadata(agent) && (
    agent.venue !== 'aster' || (!!agent.expiresAt && Date.parse(agent.expiresAt) > Date.now())
  )
}

function validMetadata(value: Omit<EncryptedTradingAgent, 'iv' | 'ciphertext'> | StoredTradingAgent): boolean {
  return (
    (value.venue === 'hyperliquid' || value.venue === 'pacifica' || value.venue === 'aster') &&
    typeof value.ownerAddress === 'string' && value.ownerAddress.length > 0 &&
    typeof value.agentAddress === 'string' && value.agentAddress.length > 0 &&
    typeof value.authorizedAt === 'string' && Number.isFinite(Date.parse(value.authorizedAt)) &&
    (value.expiresAt === undefined || Number.isFinite(Date.parse(value.expiresAt))) &&
    (value.builderAddress === undefined || /^0x[0-9a-fA-F]{40}$/.test(value.builderAddress)) &&
    (value.builderCode === undefined || /^[A-Za-z0-9]{3,16}$/.test(value.builderCode))
  )
}

type EnvelopeMetadata = Omit<EncryptedTradingAgent, 'iv' | 'ciphertext' | 'verificationIv' | 'verification'>

function envelopeMetadata(agent: StoredTradingAgent): EnvelopeMetadata {
  return {
    version: 2,
    venue: agent.venue,
    ownerAddress: agent.ownerAddress,
    agentAddress: agent.agentAddress,
    keyAlgorithm: keyAlgorithm(agent.venue),
    authorizedAt: agent.authorizedAt,
    ...(agent.expiresAt ? { expiresAt: agent.expiresAt } : {}),
    ...(agent.builderAddress ? { builderAddress: agent.builderAddress } : {}),
    ...(agent.builderCode ? { builderCode: agent.builderCode } : {}),
  }
}

function associatedData(value: EnvelopeMetadata): Uint8Array<ArrayBuffer> {
  return new TextEncoder().encode(JSON.stringify({
    version: value.version,
    venue: value.venue,
    ownerAddress: value.ownerAddress,
    agentAddress: value.agentAddress,
    keyAlgorithm: value.keyAlgorithm,
    authorizedAt: value.authorizedAt,
    expiresAt: value.expiresAt ?? null,
    builderAddress: value.builderAddress ?? null,
    builderCode: value.builderCode ?? null,
  }))
}

function verificationData(
  value: EnvelopeMetadata,
  iv: Uint8Array<ArrayBuffer>,
  ciphertext: Uint8Array<ArrayBuffer>,
): Uint8Array<ArrayBuffer> {
  const metadata = associatedData(value)
  const data = new Uint8Array(metadata.length + iv.length + ciphertext.length)
  data.set(metadata)
  data.set(iv, metadata.length)
  data.set(ciphertext, metadata.length + iv.length)
  return data
}

function validEnvelope(
  value: unknown,
  venue: Venue,
  ownerAddress: string,
): value is EncryptedTradingAgent {
  if (!value || typeof value !== 'object') return false
  const envelope = value as Partial<EncryptedTradingAgent>
  return (
    envelope.version === 2 && envelope.venue === venue &&
    envelope.ownerAddress === normalizeOwner(venue, ownerAddress) &&
    envelope.keyAlgorithm === keyAlgorithm(venue) && validMetadata(envelope as EncryptedTradingAgent) &&
    envelope.iv instanceof Uint8Array && envelope.iv.length === 12 &&
    envelope.ciphertext instanceof Uint8Array && envelope.ciphertext.length > 0 &&
    envelope.verificationIv instanceof Uint8Array && envelope.verificationIv.length === 12 &&
    envelope.verification instanceof Uint8Array && envelope.verification.length > 0 &&
    (venue !== 'aster' || (!!envelope.expiresAt && Date.parse(envelope.expiresAt) > Date.now()))
  )
}

function validMasterKey(value: unknown): value is CryptoKey {
  if (!value || typeof value !== 'object') return false
  const key = value as Partial<CryptoKey>
  const algorithm = key.algorithm as AesKeyAlgorithm | undefined
  return key.type === 'secret' && key.extractable === false && algorithm?.name === 'AES-GCM' && algorithm.length === 256 &&
    Array.isArray(key.usages) && key.usages.includes('encrypt') && key.usages.includes('decrypt')
}

function authenticationFailed(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as { name?: unknown }).name === 'OperationError'
}

function randomIV(cryptography: Crypto, excluded?: Uint8Array<ArrayBuffer>): Uint8Array<ArrayBuffer> {
  let iv: Uint8Array<ArrayBuffer>
  do {
    iv = cryptography.getRandomValues(new Uint8Array(12))
  } while (excluded && iv.every((byte, index) => byte === excluded[index]))
  return iv
}

function openDatabase(indexedDB: IDBFactory): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, databaseVersion)
    request.onupgradeneeded = () => {
      const database = request.result
      if (!database.objectStoreNames.contains(agentStoreName)) database.createObjectStore(agentStoreName)
      if (!database.objectStoreNames.contains(keyStoreName)) database.createObjectStore(keyStoreName)
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('Unable to open trading agent vault'))
    request.onblocked = () => reject(new Error('Trading agent vault upgrade is blocked'))
  })
}

function readValue(database: IDBDatabase, storeName: string, key: IDBValidKey): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const request = database.transaction(storeName).objectStore(storeName).get(key)
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('Unable to read trading agent vault'))
  })
}

function writeValue(database: IDBDatabase, storeName: string, key: IDBValidKey, value: unknown): Promise<void> {
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(storeName, 'readwrite')
    transaction.objectStore(storeName).put(value, key)
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error ?? new Error('Unable to write trading agent vault'))
    transaction.onabort = () => reject(transaction.error ?? new Error('Trading agent vault write was aborted'))
  })
}

function deleteValue(database: IDBDatabase, storeName: string, key: IDBValidKey): Promise<void> {
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(storeName, 'readwrite')
    transaction.objectStore(storeName).delete(key)
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error ?? new Error('Unable to clear trading agent vault'))
    transaction.onabort = () => reject(transaction.error ?? new Error('Trading agent vault clear was aborted'))
  })
}
