import { ComputeBudgetProgram, Message, PublicKey, Transaction } from '@solana/web3.js'
import bs58 from 'bs58'
import nacl from 'tweetnacl'
import type { SignedAction, SigningRequest } from '../types/signing.ts'

// Pinned to phoenix-rise-ix 0.6.13. Only native DelegateTrader with the fixed
// zero-priority-fee compute budget is approved here.
export const phoenixProgram = 'EtrnLzgbS7nMMy5fbD42kXiUzGg8XQzJ972Xtk1cjWih'
export const phoenixLogAuthority = 'GdxfTLSsdSY37G6fZoYtdGDSfgFnbT2EmRpuePZxWShS'
export const phoenixGlobalConfiguration = '2zskx2iyCvb6Stg7RBZkt1f6MrF4dpYtMG3yMvKwqtUZ'
const delegationBudget = [
  ComputeBudgetProgram.setComputeUnitLimit({ units: 200_000 }),
  ComputeBudgetProgram.setComputeUnitPrice({ microLamports: 0 }),
]

export type PhoenixAction = 'delegate' | 'revoke'
export type OwnerTransactionSigner = (transaction: Transaction) => Promise<Transaction>

export function phoenixTrader(owner: string): string {
  return PublicKey.findProgramAddressSync([
    new TextEncoder().encode('trader'), new PublicKey(owner).toBytes(), new Uint8Array([0, 0]),
  ], new PublicKey(phoenixProgram))[0].toBase58()
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid Phoenix signing payload')
  return value as Record<string, unknown>
}

function equal(left: Uint8Array, right: Uint8Array): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index])
}

export function fromBase64(value: string): Uint8Array {
  return Uint8Array.from(atob(value), (character) => character.charCodeAt(0))
}

export async function validatePhoenixDelegation(request: SigningRequest, expected: {
  owner: string; agent: string; action: PhoenixAction; now?: number
}): Promise<Uint8Array> {
  const now = expected.now ?? Date.now()
  const target = expected.action === 'revoke' ? expected.owner : expected.agent
  if (!request.id || request.client_order_id !== request.id || request.venue !== 'phoenix' ||
    request.action !== expected.action || request.account !== expected.owner || request.signer !== expected.owner ||
    expected.agent === expected.owner || !Number.isFinite(Date.parse(request.expires_at)) ||
    Date.parse(request.expires_at) <= now || Date.parse(request.expires_at) > now + 60_000) {
    throw new Error('Phoenix delegation does not match the owner, action or expiry')
  }
  const payload = object(request.unsigned_payload)
  const metadata = object(request.venue_metadata)
  if (payload.action !== expected.action || payload.owner_authority !== expected.owner || payload.new_position_authority !== target ||
    typeof payload.message_base64 !== 'string' || payload.message_base64.length > 1472 ||
    typeof payload.fee_payer !== 'string' || payload.fee_payer === expected.owner || payload.fee_payer === expected.agent ||
    payload.trader_account !== phoenixTrader(expected.owner) || metadata.trader_account !== payload.trader_account ||
    metadata.message_base64 !== payload.message_base64 || metadata.trader_pda_index !== 0 || metadata.subaccount_index !== 0 ||
    !Number.isSafeInteger(metadata.last_valid_block_height) || Number(metadata.last_valid_block_height) <= 0) {
    throw new Error('Phoenix delegation target or transaction metadata mismatch')
  }
  const bytes = fromBase64(payload.message_base64)
  if (bytes.length + 129 > 1232) throw new Error('Phoenix transaction exceeds packet limit')
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', new Uint8Array(bytes)))
  const hex = [...digest].map((byte) => byte.toString(16).padStart(2, '0')).join('')
  if (metadata.message_sha256 !== hex) throw new Error('Phoenix message digest mismatch')
  const signers = metadata.required_signers
  if (!Array.isArray(signers) || signers.length !== 2 ||
    object(signers[0]).index !== 0 || object(signers[0]).pubkey !== payload.fee_payer || object(signers[0]).role !== 'fee_payer' ||
    object(signers[1]).index !== 1 || object(signers[1]).pubkey !== expected.owner || object(signers[1]).role !== 'owner_authority') {
    throw new Error('Phoenix delegation requires fee payer and owner signers')
  }
  const message = Message.from(bytes)
  const keys = message.accountKeys.map((key) => key.toBase58())
  const allowed = new Set([payload.fee_payer, expected.owner, payload.trader_account, phoenixProgram, phoenixLogAuthority, phoenixGlobalConfiguration, target, ComputeBudgetProgram.programId.toBase58()])
  if (!equal(message.serialize(), bytes) || message.header.numRequiredSignatures !== 2 || message.header.numReadonlySignedAccounts !== 1 ||
    keys[0] !== payload.fee_payer || keys[1] !== expected.owner || keys.length !== allowed.size ||
    keys.some((key, index) => !allowed.has(key) || message.isAccountWritable(index) !== (key === payload.fee_payer || key === payload.trader_account)) ||
    message.recentBlockhash !== metadata.recent_blockhash || message.instructions.length !== 3) {
    throw new Error('Unexpected Phoenix transaction message or signer layout')
  }
  for (const [index, wanted] of delegationBudget.entries()) {
    const instruction = message.instructions[index]
    if (keys[instruction.programIdIndex] !== wanted.programId.toBase58() || instruction.accounts.length !== 0 ||
      !equal(bs58.decode(instruction.data), wanted.data)) {
      throw new Error('Unexpected Phoenix delegation compute budget')
    }
  }
  const instruction = message.instructions[2]
  const accounts = instruction.accounts.map((index) => keys[index])
  const wanted = [phoenixProgram, phoenixLogAuthority, phoenixGlobalConfiguration, expected.owner, payload.trader_account, target]
  const discriminator = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode('global:delegate_trader'))).slice(0, 8)
  if (keys[instruction.programIdIndex] !== phoenixProgram || accounts.length !== wanted.length ||
    accounts.some((key, index) => key !== wanted[index]) || !equal(bs58.decode(instruction.data), discriminator)) {
    throw new Error('Only the approved native Phoenix DelegateTrader instruction may be signed')
  }
  return bytes
}

export async function signPhoenixDelegation(request: SigningRequest, options: {
  owner: string; agent: string; action: PhoenixAction; signTransaction: OwnerTransactionSigner; isCurrent: () => boolean
}): Promise<SignedAction> {
  const assertOwner = () => { if (!options.isCurrent()) throw new Error('Phoenix owner changed during authorization') }
  assertOwner()
  const bytes = await validatePhoenixDelegation(request, options)
  assertOwner()
  const signed = await options.signTransaction(Transaction.populate(Message.from(bytes)))
  assertOwner()
  if (!equal(signed.serializeMessage(), bytes)) {
    throw new Error('Wallet changed the Phoenix message; the transaction was not submitted')
  }
  if (Date.parse(request.expires_at) <= Date.now()) {
    throw new Error('Phoenix signing request expired; please try again and approve within 60 seconds')
  }
  const signature = signed.signatures.find((entry) => entry.publicKey.toBase58() === options.owner)?.signature
  if (!signature || !nacl.sign.detached.verify(bytes, signature, new PublicKey(options.owner).toBytes())) {
    throw new Error('Invalid Phoenix owner signature')
  }
  return { request_id: request.id, client_order_id: request.client_order_id, venue: 'phoenix', signer_address: options.owner, signature: bs58.encode(signature) }
}
