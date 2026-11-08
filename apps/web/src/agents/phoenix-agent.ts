import bs58 from 'bs58'
import nacl from 'tweetnacl'
import { apiError, apiFetch } from '../lib/api.ts'
import type { SignedAction, SigningRequest } from '../types/signing.ts'
import { signPhoenixDelegation, type OwnerTransactionSigner, type PhoenixAction } from './phoenix-message.ts'
import type { TradingAgentStore } from './storage.ts'
import type { PhoenixOperation, StoredTradingAgent } from './types.ts'

export interface PhoenixResult {
  request_id: string
  owner: string
  action: PhoenixAction
  target_authority: string
  state: 'prepared' | 'pending' | 'confirmed' | 'failed' | 'expired' | 'superseded'
  signing_request?: SigningRequest
}

export interface PhoenixAuthority {
  owner: string; agent: string; position_authority: string; authorized: boolean
}

export interface PhoenixTransport {
  available?(): Promise<void>
  prepare(owner: string, agent: string, action: PhoenixAction, replace: boolean, expectedAuthority?: string): Promise<PhoenixResult>
  submit(owner: string, signed: SignedAction): Promise<PhoenixResult>
  reconcile(owner: string, requestID: string): Promise<PhoenixResult>
  authority(owner: string, agent: string): Promise<PhoenixAuthority>
}

export class PhoenixPendingError extends Error { override name = 'PhoenixPendingError' }
export class PhoenixReplacementRequired extends Error {
  override name = 'PhoenixReplacementRequired'
  readonly authority: string
  constructor(authority: string) { super('Confirm replacement of the existing Phoenix delegate'); this.authority = authority }
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid Phoenix response')
  return value as Record<string, unknown>
}

export function createPhoenixTransport(): PhoenixTransport {
  const post = async (operation: string, request: unknown): Promise<unknown> => {
    const response = await apiFetch(`/api/v1/live/phoenix/delegation/${operation}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(request),
      signal: AbortSignal.timeout(35_000),
    })
    const body: unknown = await response.json()
    const value = record(body)
    if (!response.ok && !('state' in value)) {
      if (operation === 'prepare' && value.error === 'confirm replacement of existing Phoenix delegate') {
        const current = record(value.result).current_authority
        if (typeof current === 'string') throw new PhoenixReplacementRequired(current)
      }
      throw apiError(response.status, 'Unable to verify Phoenix authorization', body)
    }
    return body
  }
  const result = async (operation: string, request: unknown): Promise<PhoenixResult> => {
    const value = record(await post(operation, request))
    if (typeof value.request_id !== 'string' || !value.request_id || typeof value.owner !== 'string' ||
      !['delegate', 'revoke'].includes(String(value.action)) || typeof value.target_authority !== 'string' ||
      !['prepared', 'pending', 'confirmed', 'failed', 'expired', 'superseded'].includes(String(value.state))) {
      throw new Error('Invalid Phoenix delegation result')
    }
    return value as unknown as PhoenixResult
  }
  return {
    available: async () => {
      const response = await apiFetch('/api/v1/live/phoenix/status', { signal: AbortSignal.timeout(15_000) })
      if (!response.ok) throw apiError(response.status, 'Phoenix infrastructure is unavailable')
      const body = record(await response.json())
      if (body.configured !== true || record(body.infrastructure).ready !== true) throw new Error('Phoenix infrastructure is not ready. No agent has been created.')
    },
    prepare: (owner, agent, action, replace, expectedAuthority) => result('prepare', { owner, agent, action, replace_existing_delegate: replace, ...(expectedAuthority ? { expected_position_authority: expectedAuthority } : {}) }),
    submit: (owner, signed) => result('submit', { owner, signed_action: signed }),
    reconcile: (owner, request_id) => result('reconcile', { owner, request_id }),
    authority: async (owner, agent) => {
      const value = record(await post('authority', { owner, agent }))
      if (value.owner !== owner || value.agent !== agent || typeof value.position_authority !== 'string' ||
        bs58.decode(value.position_authority).length !== 32 || typeof value.authorized !== 'boolean') throw new Error('Invalid Phoenix authority response')
      return value as unknown as PhoenixAuthority
    },
  }
}

interface Options {
  storage: TradingAgentStore
  ownerAddress: string
  transport: PhoenixTransport
  isCurrent: () => boolean
  signTransaction?: OwnerTransactionSigner
  replaceExistingDelegate?: boolean
  expectedAuthority?: string
  wait?: () => Promise<void>
  clearCandidates?: string[]
}

function assertOwner(options: Options): void {
  if (!options.isCurrent()) throw new Error('Phoenix owner changed during authorization')
}

function assertResult(value: PhoenixResult, owner: string, operation: PhoenixOperation): void {
  if (value.owner !== owner || value.request_id !== operation.requestID || value.action !== operation.action || value.target_authority !== operation.targetAuthority) {
    throw new Error('Phoenix result does not match the stored owner and operation')
  }
}

async function persist(options: Options, agent: StoredTradingAgent, pending: boolean): Promise<void> {
  assertOwner(options)
  if (pending) await options.storage.updatePending(agent)
  else await options.storage.save(agent)
  assertOwner(options)
}

async function reconcile(options: Options, agent: Pick<StoredTradingAgent, 'phoenixOperation'>): Promise<PhoenixResult> {
  const operation = agent.phoenixOperation!
  const deadline = Date.now() + 20_000
  for (let attempt = 0; ; attempt++) {
    assertOwner(options)
    const value = await options.transport.reconcile(options.ownerAddress, operation.requestID)
    assertOwner(options)
    assertResult(value, options.ownerAddress, operation)
    if (!['pending', 'prepared'].includes(value.state)) return value
    if (value.state === 'prepared' && !operation.submissionStarted) return value
    if (attempt >= 5 || Date.now() >= deadline) throw new PhoenixPendingError('Phoenix confirmation is pending. Resume to verify; your key is preserved.')
    await (options.wait?.() ?? new Promise<void>((resolve) => setTimeout(resolve, 750)))
  }
}

async function clearAfterRevoke(options: Options, agent: Pick<StoredTradingAgent, 'agentAddress'>, result: PhoenixResult): Promise<void> {
  if (result.state !== 'confirmed' || result.action !== 'revoke' || result.target_authority !== options.ownerAddress) throw new Error('Phoenix revoke is not confirmed')
  assertOwner(options)
  for (const candidate of new Set([agent.agentAddress, ...(options.clearCandidates ?? [])])) {
    await options.storage.clear('phoenix', options.ownerAddress, candidate)
    await options.storage.clearPending('phoenix', options.ownerAddress, candidate)
  }
  assertOwner(options)
}

async function promote(options: Options, agent: StoredTradingAgent): Promise<StoredTradingAgent> {
  assertOwner(options)
  const active = { ...agent, authorizedAt: new Date().toISOString() }
  await options.storage.promotePending(active)
  assertOwner(options)
  return active
}

export async function restorePhoenixAgent(options: Options): Promise<Omit<StoredTradingAgent, 'privateKey'> | null> {
  const pending = await options.storage.loadPendingForSigning('phoenix', options.ownerAddress)
  const active = await options.storage.restore('phoenix', options.ownerAddress)
  options = { ...options, clearCandidates: [pending?.agentAddress, active?.agentAddress].filter((value): value is string => !!value) }
  assertOwner(options)
  for (const agent of [pending, active]) {
    if (!agent) continue
    const operation = agent.phoenixOperation
    if (agent === pending || operation?.action === 'revoke') {
      if (!operation) throw new PhoenixPendingError('Phoenix pending key needs owner authorization. Resume to continue.')
      const value = await reconcile(options, agent)
      if (value.state === 'confirmed' && operation.action === 'revoke') { await clearAfterRevoke(options, agent, value); return null }
      if (value.state === 'confirmed' && agent === pending && pending && operation.action === 'delegate') {
        await promote(options, pending)
        return options.storage.restore('phoenix', options.ownerAddress)
      }
      if (value.state === 'prepared') throw new PhoenixPendingError('Phoenix authorization needs an owner signature. Resume to continue.')
      if (agent === pending) throw new PhoenixPendingError('Phoenix authorization was not completed. Resume or revoke; your pending key is preserved.')
    }
    const authority = await options.transport.authority(options.ownerAddress, agent.agentAddress)
    assertOwner(options)
    if (authority.authorized && authority.position_authority === agent.agentAddress) return agent
    throw new Error('Phoenix authority no longer matches this local agent. Reauthorize or revoke; the key is preserved.')
  }
  return null
}

async function runOperation(options: Options, agent: StoredTradingAgent, pending: boolean, action: PhoenixAction): Promise<StoredTradingAgent | null> {
  assertOwner(options)
  let prepared: PhoenixResult | undefined
  if (agent.phoenixOperation) {
    const value = await reconcile(options, agent)
    if (value.state === 'confirmed' && agent.phoenixOperation.action === 'revoke') { await clearAfterRevoke(options, agent, value); return null }
    if (value.state === 'confirmed' && pending && action === 'delegate' && agent.phoenixOperation.action === 'delegate') return promote(options, agent)
    if (value.state === 'prepared') {
      if (agent.phoenixOperation.action !== action) throw new PhoenixPendingError('The previous unsigned Phoenix request must expire before changing action. Retry shortly.')
      prepared = value
    }
  }
  if (!prepared) prepared = await options.transport.prepare(options.ownerAddress, agent.agentAddress, action, options.replaceExistingDelegate ?? false, options.expectedAuthority)
  assertOwner(options)
  const operation: PhoenixOperation = {
    requestID: prepared.request_id, action, targetAuthority: action === 'revoke' ? options.ownerAddress : agent.agentAddress, submissionStarted: false,
  }
  assertResult(prepared, options.ownerAddress, operation)
  if (prepared.state !== 'prepared' || !prepared.signing_request) throw new PhoenixPendingError('Phoenix operation is unresolved. Verify it before signing again.')
  if (prepared.signing_request.id !== prepared.request_id) throw new Error('Phoenix signing request correlation mismatch')
  agent = { ...agent, phoenixOperation: operation }
  await persist(options, agent, pending)
  if (!options.signTransaction) throw new Error('Solana wallet does not support transaction signing')
  const signed = await signPhoenixDelegation(prepared.signing_request, {
    owner: options.ownerAddress, agent: agent.agentAddress, action, signTransaction: options.signTransaction, isCurrent: options.isCurrent,
  })
  agent = { ...agent, phoenixOperation: { ...operation, submissionStarted: true } }
  await persist(options, agent, pending)
  try {
    const submitted = await options.transport.submit(options.ownerAddress, signed)
    assertResult(submitted, options.ownerAddress, agent.phoenixOperation!)
  } catch {
    // A lost response must only trigger reconciliation, never a second send.
  }
  assertOwner(options)
  const result = await reconcile(options, agent)
  if (result.state !== 'confirmed') throw new PhoenixPendingError(`Phoenix ${result.state}; resume or revoke. Your key is preserved.`)
  if (action === 'revoke') { await clearAfterRevoke(options, agent, result); return null }
  return promote(options, agent)
}

export async function authorizePhoenixAgent(options: Options): Promise<StoredTradingAgent> {
  await options.storage.ready()
  assertOwner(options)
  let agent = await options.storage.loadPendingForSigning('phoenix', options.ownerAddress)
  const active = await options.storage.loadForSigning('phoenix', options.ownerAddress)
  options = { ...options, clearCandidates: [agent?.agentAddress, active?.agentAddress].filter((value): value is string => !!value) }
  if (!agent) {
    await options.transport.available?.()
    assertOwner(options)
    if (active?.phoenixOperation?.action === 'revoke') {
      const previous = await reconcile(options, active)
      if (previous.state === 'confirmed') await clearAfterRevoke(options, active, previous)
      else if (previous.state === 'prepared') await runOperation(options, active, false, 'revoke')
    }
    assertOwner(options)
    const generated = nacl.sign.keyPair()
    agent = { version: 2, venue: 'phoenix', ownerAddress: options.ownerAddress, agentAddress: bs58.encode(generated.publicKey), privateKey: bs58.encode(generated.secretKey), authorizedAt: new Date().toISOString() }
    await options.storage.savePending(agent)
  }
  const result = await runOperation(options, agent, true, 'delegate')
  if (!result) throw new PhoenixPendingError('Phoenix revoke completed. Authorize a new agent to continue.')
  return result
}

export async function revokePhoenixAgent(options: Options): Promise<void> {
  await options.storage.ready()
  assertOwner(options)
  const pending = await options.storage.loadPendingForSigning('phoenix', options.ownerAddress)
  const active = await options.storage.loadForSigning('phoenix', options.ownerAddress)
  options = { ...options, clearCandidates: [pending?.agentAddress, active?.agentAddress].filter((value): value is string => !!value) }
  // A replacement cancelled before prepare has never been submitted. The active
  // delegate still owns authority; retain both keys until its revoke confirms.
  const agent = pending?.phoenixOperation ? pending : active ?? pending
  if (!agent) return
  await runOperation(options, agent, agent === pending, 'revoke')
}
