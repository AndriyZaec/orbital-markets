import { Message, PublicKey } from '@solana/web3.js'
import bs58 from 'bs58'
import nacl from 'tweetnacl'
import type { SignedAction, SigningRequest } from '../types/signing.ts'
import type { StoredTradingAgent } from './types.ts'
import { fromBase64, phoenixGlobalConfiguration, phoenixLogAuthority, phoenixProgram, phoenixTrader } from './phoenix-message.ts'

// Semantic validator for the exact market-order template emitted by Rise IX
// 0.6.13, prefixed by exactly two bounded Compute Budget instructions.
export const phoenixFlightProgram = 'F1ightu9cujFYo34k9CabifLrJT8qzfDVM2Q7BqhJn2W'
const computeProgram = 'ComputeBudget111111111111111111111111111111'
const encoder = new TextEncoder()
const u64Max = (1n << 64n) - 1n

function requireOrder(condition: unknown, detail: string): asserts condition {
  if (!condition) throw new Error(`Invalid Phoenix order: ${detail}`)
}

function record(value: unknown): Record<string, unknown> {
  requireOrder(value && typeof value === 'object' && !Array.isArray(value), 'payload object')
  return value as Record<string, unknown>
}

function equal(left: Uint8Array, right: Uint8Array): boolean {
  return left.length === right.length && left.every((byte, index) => byte === right[index])
}

function uint(value: unknown, max = u64Max): bigint {
  requireOrder(typeof value === 'string' && /^[1-9][0-9]{0,19}$/.test(value), 'canonical native integer')
  const result = BigInt(value)
  requireOrder(result <= max, 'native integer range')
  return result
}

function positive(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

function safePositive(value: unknown): value is number {
  return positive(value) && Number.isSafeInteger(value)
}

function key(value: unknown): string {
  requireOrder(typeof value === 'string' && value.length >= 32 && value.length <= 44, 'account key')
  requireOrder(new PublicKey(value).toBase58() === value, 'canonical account key')
  return value
}

function pda(program: string, seeds: Uint8Array[]): string {
  return PublicKey.findProgramAddressSync(seeds, new PublicKey(program))[0].toBase58()
}

function scaled(coefficient: bigint, exponent: number): [bigint, bigint] {
  return exponent >= 0 ? [coefficient * 10n ** BigInt(exponent), 1n] : [coefficient, 10n ** BigInt(-exponent)]
}

// Verify SDK units exactly; Number is used only for the existing UI summary.
function decimalMatches(value: unknown, coefficient: bigint, exponent: number): value is string {
  requireOrder(typeof value === 'string' && value.length <= 96 && /^(0|[1-9][0-9]*)(\.[0-9]{1,28})?$/.test(value), 'decimal units')
  const [whole, fraction = ''] = value.split('.')
  const [numerator, denominator] = scaled(coefficient, exponent)
  return BigInt(whole + fraction) * denominator === numerator * 10n ** BigInt(fraction.length)
}

function le(value: bigint, length: number): number[] {
  return Array.from({ length }, (_, index) => Number((value >> BigInt(index * 8)) & 255n))
}

async function discriminator(name: string): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(`global:${name}`))).slice(0, 8)
}

function validTime(request: SigningRequest, now: number): void {
  const created = Date.parse(request.created_at), expires = Date.parse(request.expires_at)
  requireOrder(Number.isFinite(created) && Number.isFinite(expires) && created <= now + 2_000 &&
    expires > now && expires > created && expires <= created + 30_000 && expires <= now + 30_000, 'expiry')
}

export async function validatePhoenixOrder(request: SigningRequest, expected: {
  owner: string; agent: string; now?: number
}): Promise<Uint8Array> {
  const now = expected.now ?? Date.now()
  const owner = key(expected.owner), agent = key(expected.agent)
  requireOrder(request.venue === 'phoenix' && request.account === owner && request.signer === agent && owner !== agent &&
    typeof request.id === 'string' && request.id.length > 0 && request.id.length <= 128 &&
    ['open', 'close', 'unwind', 'emergency_close'].includes(request.action) &&
    (request.side === 'buy' || request.side === 'sell') && typeof request.symbol === 'string' &&
    encoder.encode(request.symbol).length > 0 && encoder.encode(request.symbol).length <= 32 &&
    positive(request.amount) && positive(request.price) && positive(request.limit_price) &&
    request.reduce_only === (request.action !== 'open'), 'intent, authority or summary')
  validTime(request, now)
  const payload = record(request.unsigned_payload), metadata = record(request.venue_metadata)
  const built = record(metadata.built), manifest = record(built.manifest), normalized = record(metadata.normalized)
  const payer = key(payload.fee_payer), trader = phoenixTrader(owner)
  const cid = uint(request.client_order_id), lots = uint(payload.num_base_lots, 0xffff_ffffn)
  const ticks = uint(payload.price_limit_ticks, 0xffff_ffffn), slot = uint(payload.last_valid_slot)
  const side = request.side === 'buy' ? 'bid' : 'ask'
  requireOrder(payload.format === 'solana_legacy_message' && manifest.format === payload.format &&
    payload.owner_authority === owner && payload.position_authority === agent && payload.trader_account === trader &&
    payer !== owner && payer !== agent && payload.client_order_id === request.client_order_id &&
    typeof payload.message_base64 === 'string' && payload.message_base64.length <= 1472 &&
    payload.message_base64 === built.message_base64 && payload.message_sha256 === built.message_sha256 &&
    metadata.protocol_version === 1 && built.trader_account === trader && built.trader_pda_index === 0 && built.subaccount_index === 0 &&
    safePositive(metadata.min_context_slot) && slot > BigInt(metadata.min_context_slot) && slot <= BigInt(metadata.min_context_slot) + 64n &&
    safePositive(metadata.block_height_at_preparation) && safePositive(built.last_valid_block_height) &&
    metadata.block_height_at_preparation < built.last_valid_block_height, 'transaction metadata')
  requireOrder(manifest.phoenix_program_id === phoenixProgram && manifest.intent_action === request.action &&
    manifest.owner_authority === owner && manifest.position_authority === agent && manifest.fee_payer === payer &&
    manifest.trader_account === trader && manifest.requested_symbol === request.symbol && manifest.side === side &&
    manifest.reduce_only === request.reduce_only && manifest.num_base_lots === Number(lots) && manifest.price_limit_ticks === Number(ticks) &&
    // These diagnostic JSON u64s may be rounded. Payload strings and actual
    // instruction bytes below are the authoritative exact CID/slot binding.
    manifest.client_order_id === Number(cid) && manifest.last_valid_slot === Number(slot) &&
    safePositive(built.metadata_generation) && built.metadata_generation === manifest.metadata_generation &&
    built.metadata_generation === normalized.metadata_generation && normalized.symbol === request.symbol && normalized.side === side &&
    normalized.num_base_lots === Number(lots) && normalized.price_limit_ticks === Number(ticks), 'manifest binding')
  requireOrder(typeof normalized.base_lot_decimals === 'number' && Number.isInteger(normalized.base_lot_decimals) &&
    normalized.base_lot_decimals >= -18 && normalized.base_lot_decimals <= 18, 'lot precision')
  const decimals = normalized.base_lot_decimals, tickSize = uint(normalized.tick_size)
  requireOrder(decimalMatches(normalized.amount, lots, -decimals) && Number(normalized.amount) === request.amount &&
    decimalMatches(normalized.limit_price, ticks * tickSize, decimals - 6) && Number(normalized.limit_price) === request.limit_price, 'amount or limit units')

  const bytes = fromBase64(payload.message_base64)
  requireOrder(bytes.length > 0 && bytes.length + 129 <= 1232 && (bytes[0] & 128) === 0, 'legacy packet size')
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', new Uint8Array(bytes)))
  requireOrder(payload.message_sha256 === [...digest].map((byte) => byte.toString(16).padStart(2, '0')).join(''), 'message digest')
  const message = Message.from(bytes), keys = message.accountKeys.map((account) => account.toBase58())
  requireOrder(equal(message.serialize(), bytes) && message.header.numRequiredSignatures === 2 &&
    message.header.numReadonlySignedAccounts === 1 && keys[0] === payer && keys[1] === agent &&
    message.recentBlockhash === built.recent_blockhash && message.instructions.length === 3, 'message or signer layout')
  const budget = record(manifest.compute_budget)
  requireOrder(safePositive(budget.unit_limit) && budget.unit_limit <= 1_400_000 &&
    typeof budget.unit_price_micro_lamports === 'number' && Number.isSafeInteger(budget.unit_price_micro_lamports) &&
    budget.unit_price_micro_lamports >= 0 && budget.unit_price_micro_lamports <= 1_000_000, 'compute budget bounds')
  const computeData = [new Uint8Array([2, ...le(BigInt(budget.unit_limit), 4)]),
    new Uint8Array([3, ...le(BigInt(budget.unit_price_micro_lamports), 8)])]
  for (const [index, wanted] of computeData.entries()) {
    const ix = message.instructions[index]
    requireOrder(keys[ix.programIdIndex] === computeProgram && ix.accounts.length === 0 &&
      equal(bs58.decode(ix.data), wanted), 'canonical compute budget instructions')
  }
  requireOrder(Array.isArray(built.required_signers) && built.required_signers.length === 2, 'required signers')
  for (const [index, role] of ['fee_payer', 'position_authority'].entries()) {
    const signer = record(built.required_signers[index])
    requireOrder(signer.index === index && signer.pubkey === keys[index] && signer.role === role, 'signer manifest')
  }
  const instruction = message.instructions[2]
  const accounts = instruction.accounts.map((index) => {
    requireOrder(Number.isInteger(index) && index >= 0 && index < keys.length, 'account index')
    return keys[index]
  })
  let data = bs58.decode(instruction.data), native = accounts
  const readonly = new Set([phoenixProgram, phoenixLogAuthority, agent, computeProgram])
  const writable = new Set([payer])
  const flight = request.action === 'open' || request.action === 'close'
  if (flight) {
    const f = record(manifest.flight), builder = key(f.builder_authority)
    const programBytes = new PublicKey(phoenixProgram).toBytes()
    const transfer = pda(phoenixFlightProgram, [encoder.encode('collateral_transfer_authority'), programBytes])
    const prefix = [pda(phoenixFlightProgram, [programBytes, encoder.encode('global_state')]), phoenixProgram, builder,
      phoenixTrader(builder), pda(phoenixFlightProgram, [programBytes, new PublicKey(builder).toBytes(), encoder.encode('builder_state')]), agent]
    const tail = [transfer, pda(phoenixProgram, [encoder.encode('permission'), new PublicKey(owner).toBytes(), new PublicKey(transfer).toBytes()])]
    requireOrder(manifest.routing === 'flight' && keys[instruction.programIdIndex] === phoenixFlightProgram &&
      f.flight_program_id === phoenixFlightProgram && f.builder_trader_account === prefix[3] && f.builder_state === prefix[4] &&
      f.builder_pda_index === 0 && f.builder_subaccount_index === 0 && f.expected_registered_fee_bps === 2 && f.uses_fee_override === false &&
      prefix.every((account, index) => accounts[index] === account) &&
      tail.every((account, index) => accounts[accounts.length - 2 + index] === account) &&
      equal(data.slice(0, 8), await discriminator('proxy_instruction')), 'Flight proxy or delegated permission')
    // Registration/actual 2 bps verification is backend chain readiness; the
    // browser enforces the canonical non-override instruction and PDA layout.
    for (const index of [0, 1, 2, 4, 5]) readonly.add(prefix[index])
    readonly.add(phoenixFlightProgram); readonly.add(transfer)
    writable.add(prefix[3]); writable.add(tail[1])
    native = accounts.slice(6, -2)
    data = data.slice(8)
  } else {
    requireOrder(manifest.routing === 'native' && manifest.flight === null && keys[instruction.programIdIndex] === phoenixProgram, 'native safety routing')
  }
  requireOrder(native.length >= 10 && native.length <= 136 &&
    native[0] === phoenixProgram && native[1] === phoenixLogAuthority && native[2] === phoenixGlobalConfiguration &&
    native[3] === agent && native[4] === trader && native[native.length - 2] === manifest.orderbook, 'native market accounts')
  for (const [index, account] of native.entries()) if (index !== 0 && index !== 1 && index !== 3) writable.add(account)
  requireOrder([...writable].every((account) => !readonly.has(account)), 'account privilege alias')
  const allowed = new Set([...readonly, ...writable])
  requireOrder(keys.length === allowed.size && new Set(keys).size === keys.length &&
    keys.every((account, index) => allowed.has(account) && message.isAccountWritable(index) === writable.has(account)), 'unexpected account or writable privilege')

  // Exact Borsh IOC packet: bounded price/base size, no quote size/minimums,
  // Abort self-trade behavior, no match limit, u128 CID (upper half zero),
  // Some(last_valid_slot), reduce-only flag, cancel_existing=false.
  const wanted = new Uint8Array([
    ...await discriminator('place_market_order'), 2, request.side === 'buy' ? 0 : 1,
    1, ...le(ticks, 8), ...le(lots, 8), 0, ...le(0n, 8), ...le(0n, 8), 0, 0,
    ...le(cid, 16), 1, ...le(slot, 8), request.reduce_only ? 128 : 0, 0,
  ])
  requireOrder(equal(data, wanted), 'only the approved bounded IOC order may be signed')
  requireOrder(Array.isArray(manifest.account_keys) && manifest.account_keys.length === keys.length &&
    Array.isArray(manifest.instructions) && manifest.instructions.length === 3, 'instruction manifest')
  for (const [index, account] of manifest.account_keys.entries()) {
    const row = record(account)
    requireOrder(row.index === index && row.pubkey === keys[index] && row.signer === (index < 2) &&
      row.writable === message.isAccountWritable(index), 'account manifest')
  }
  for (const [index, ix] of message.instructions.entries()) {
    const described = record(manifest.instructions[index])
    requireOrder(described.program_id === keys[ix.programIdIndex] && described.program_id_index === ix.programIdIndex &&
      Array.isArray(described.account_indexes) && described.account_indexes.length === ix.accounts.length &&
      described.account_indexes.every((index, offset) => index === ix.accounts[offset]) &&
      typeof described.data_base64 === 'string' && described.data_base64.length <= 1472 &&
      equal(fromBase64(described.data_base64), bs58.decode(ix.data)), 'instruction bytes manifest')
  }
  validTime(request, expected.now ?? Date.now())
  return bytes
}

export async function signPhoenixAgentRequest(request: SigningRequest, agent: StoredTradingAgent, isCurrent: () => boolean = () => true): Promise<SignedAction> {
  // Capture before async digest work; never validate one object and sign a
  // caller-mutated replacement. Owner/agent lifecycle is fenced separately.
  request = structuredClone(request)
  requireOrder(isCurrent() && agent.venue === 'phoenix' && agent.phoenixOperation?.action !== 'revoke' &&
    (!agent.expiresAt || Date.parse(agent.expiresAt) > Date.now()), 'active local agent')
  const bytes = await validatePhoenixOrder(request, { owner: agent.ownerAddress, agent: agent.agentAddress })
  requireOrder(isCurrent(), 'owner changed during signing')
  const secret = bs58.decode(agent.privateKey)
  const pair = nacl.sign.keyPair.fromSeed(secret.slice(0, 32))
  requireOrder(secret.length === 64 && equal(secret, pair.secretKey) && bs58.encode(pair.publicKey) === agent.agentAddress, 'agent key does not match its address')
  validTime(request, Date.now())
  return { request_id: request.id, client_order_id: request.client_order_id, venue: 'phoenix',
    signer_address: agent.agentAddress, signature: bs58.encode(nacl.sign.detached(bytes, pair.secretKey)) }
}
