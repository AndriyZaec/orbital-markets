import type { AccessIdentity, Env } from '../../types'
import { auditInsert, findIdempotentAction } from '../audit/service'
import { jsonOk, jsonResponse, requireIdempotencyKey } from '../../shared/http'
import { acquireMutationCooldown, logMutation } from '../../shared/operations'

const CODE_ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'
const CODE_LENGTH = 12
const CODE_KEY_PREFIX = 'invite:'

interface WaitlistRow {
  id: string
  email: string
  status: string
}

interface InviteRow {
  id: string
  waitlist_entry_id: string
  code: string
  status: 'issued' | 'sent' | 'delivery_failed' | 'redeemed' | 'revoked'
  bound_cookie_id: string | null
  created_at: number
  sent_at: number | null
  redeemed_at: number | null
  revoked_at: number | null
  delivery_attempts: number
  delivery_error: string | null
  updated_at: number
}

export async function handleInviteRoute(request: Request, env: Env, actor: AccessIdentity, path: string): Promise<Response> {
  const issueMatch = path.match(/^\/waitlist\/([^/]+)\/invites$/)
  if (request.method === 'POST' && issueMatch) return issueInvite(request, env, actor, issueMatch[1])
  const actionMatch = path.match(/^\/invites\/([^/]+)\/(resend|revoke)$/)
  if (request.method === 'POST' && actionMatch) {
    return actionMatch[2] === 'resend'
      ? resendInvite(request, env, actor, actionMatch[1])
      : revokeInvite(request, env, actor, actionMatch[1])
  }
  return jsonResponse(404, 'not_found', 'Admin route not found.')
}

async function issueInvite(request: Request, env: Env, actor: AccessIdentity, entryId: string): Promise<Response> {
  const keyOrResponse = requireIdempotencyKey(request)
  if (keyOrResponse instanceof Response) return keyOrResponse
  const previous = await findIdempotentAction(env, actor, keyOrResponse)
  if (previous) {
    if (previous.action !== 'invite.issued') return jsonResponse(409, 'idempotency_key_reused', 'The Idempotency-Key was already used for another action.')
    return inviteResult(env, previous.target_id, true)
  }

  const entry = await env.WAITLIST_DB.prepare('SELECT id, email, status FROM waitlist_entries WHERE id = ?').bind(entryId).first<WaitlistRow>()
  if (!entry) return jsonResponse(404, 'waitlist_entry_not_found', 'Waitlist entry not found.')
  if (entry.status !== 'approved' && entry.status !== 'invited') return jsonResponse(409, 'invalid_state', 'Only approved waitlist entries can receive an invite.')

  let invite = await activeInvite(env, entryId)
  if (!invite) {
    if (entry.status !== 'approved') return jsonResponse(409, 'invite_not_found', 'The invited entry has no active invite to retry.')
    try {
      invite = await createInvite(env, entryId)
    } catch (error) {
      if (error instanceof InviteDeliveryError) return jsonResponse(error.status, error.code, error.message, { retryable: true })
      return jsonResponse(500, 'invite_create_failed', 'Invite could not be created.')
    }
  } else if (invite.status === 'sent' && entry.status === 'invited') {
    await env.WAITLIST_DB.batch([
      auditInsert(env, actor, 'invite.issued', 'beta_invite', invite.id, { waitlist_entry_id: entryId, reused: true }, keyOrResponse, now()),
    ])
    logMutation(request, actor, 'invite.issued', 'beta_invite', invite.id)
    return inviteResult(env, invite.id, false)
  }
  const cooldownResponse = await acquireMutationCooldown(env, actor, 'invite.issue', entryId)
  if (cooldownResponse) return cooldownResponse
  const result = await deliverInvite(env, invite, entry.email, false)
  if (!result.ok) {
    await recordDeliveryFailure(env, actor, invite.id, invite.waitlist_entry_id)
    return result.response
  }
  const timestamp = now()
  const action = auditInsert(env, actor, 'invite.issued', 'beta_invite', invite.id, { waitlist_entry_id: entryId }, keyOrResponse, timestamp)
  await env.WAITLIST_DB.batch([action])
  logMutation(request, actor, 'invite.issued', 'beta_invite', invite.id)
  return inviteResult(env, invite.id, false)
}

async function resendInvite(request: Request, env: Env, actor: AccessIdentity, inviteId: string): Promise<Response> {
  const keyOrResponse = requireIdempotencyKey(request)
  if (keyOrResponse instanceof Response) return keyOrResponse
  const previous = await findIdempotentAction(env, actor, keyOrResponse)
  if (previous) {
    if (previous.action !== 'invite.resent') return jsonResponse(409, 'idempotency_key_reused', 'The Idempotency-Key was already used for another action.')
    return inviteResult(env, previous.target_id, true)
  }
  const invite = await findInvite(env, inviteId)
  if (!invite) return jsonResponse(404, 'invite_not_found', 'Invite not found.')
  if (invite.status === 'revoked') return jsonResponse(409, 'invite_revoked', 'A disabled invite cannot be resent.')
  if (invite.status === 'redeemed') return jsonResponse(409, 'invite_redeemed', 'A redeemed invite cannot be resent.')
  const cooldownResponse = await acquireMutationCooldown(env, actor, 'invite.resend', inviteId)
  if (cooldownResponse) return cooldownResponse
  const entry = await env.WAITLIST_DB.prepare('SELECT id, email FROM waitlist_entries WHERE id = ?').bind(invite.waitlist_entry_id).first<{ id: string; email: string }>()
  if (!entry) return jsonResponse(409, 'waitlist_entry_not_found', 'The invite owner no longer exists.')
  const result = await deliverInvite(env, invite, entry.email, true)
  if (!result.ok) {
    await recordDeliveryFailure(env, actor, invite.id, invite.waitlist_entry_id)
    return result.response
  }
  await env.WAITLIST_DB.batch([
    auditInsert(env, actor, 'invite.resent', 'beta_invite', invite.id, { waitlist_entry_id: invite.waitlist_entry_id }, keyOrResponse, now()),
  ])
  logMutation(request, actor, 'invite.resent', 'beta_invite', invite.id)
  return inviteResult(env, invite.id, false)
}

async function revokeInvite(request: Request, env: Env, actor: AccessIdentity, inviteId: string): Promise<Response> {
  const keyOrResponse = requireIdempotencyKey(request)
  if (keyOrResponse instanceof Response) return keyOrResponse
  const previous = await findIdempotentAction(env, actor, keyOrResponse)
  if (previous) {
    if (previous.action !== 'invite.revoked') return jsonResponse(409, 'idempotency_key_reused', 'The Idempotency-Key was already used for another action.')
    return inviteResult(env, previous.target_id, true)
  }
  const invite = await findInvite(env, inviteId)
  if (!invite) return jsonResponse(404, 'invite_not_found', 'Invite not found.')
  const cooldownResponse = await acquireMutationCooldown(env, actor, 'invite.revoke', inviteId)
  if (cooldownResponse) return cooldownResponse
  const timestamp = now()
  const result = await env.WAITLIST_DB.prepare(
    `UPDATE beta_invites SET status = 'revoked', revoked_at = ?, updated_at = ?
      WHERE id = ? AND status != 'revoked'`,
  ).bind(timestamp, timestamp, inviteId).run()
  if (invite.status !== 'revoked' && result.meta.changes !== 1) return jsonResponse(409, 'invite_state_changed', 'The invite changed before it could be disabled.')

  try {
    await writeKVRevocation(env, invite.code, invite.waitlist_entry_id, timestamp)
  } catch (error) {
    return jsonResponse(502, 'invite_kv_sync_failed', 'Invite was disabled in D1 but KV synchronization failed. Retry the action.', { retryable: true, detail: safeError(error) })
  }
  await env.WAITLIST_DB.batch([
    auditInsert(env, actor, 'invite.revoked', 'beta_invite', invite.id, { waitlist_entry_id: invite.waitlist_entry_id }, keyOrResponse, timestamp),
  ])
  logMutation(request, actor, 'invite.revoked', 'beta_invite', invite.id)
  return inviteResult(env, invite.id, false)
}

async function createInvite(env: Env, entryId: string): Promise<InviteRow> {
  const timestamp = now()
  const id = crypto.randomUUID()
  const code = generateCode()
  await env.WAITLIST_DB.prepare(
    `INSERT INTO beta_invites
      (id, waitlist_entry_id, code, status, created_at, delivery_attempts, updated_at)
     VALUES (?, ?, ?, 'issued', ?, 0, ?)`,
  ).bind(id, entryId, code, timestamp, timestamp).run()
  try {
    await env.BETA_INVITES.put(`${CODE_KEY_PREFIX}${code}`, JSON.stringify({
      waitlist_entry_id: entryId,
      created_at: timestamp,
    }))
  } catch (error) {
    await markDeliveryFailure(env, id, safeError(error))
    throw new InviteDeliveryError('invite_kv_sync_failed', 'Invite could not be written to KV.', 502)
  }
  const invite = await findInvite(env, id)
  if (!invite) throw new InviteDeliveryError('invite_not_found', 'Invite could not be loaded after creation.', 500)
  return invite
}

async function deliverInvite(env: Env, invite: InviteRow, email: string, forceSend: boolean): Promise<{ ok: true } | { ok: false; response: Response }> {
  const attempt = invite.delivery_attempts + 1
  const attemptResult = await env.WAITLIST_DB.prepare(
    'UPDATE beta_invites SET delivery_attempts = ?, updated_at = ? WHERE id = ?',
  ).bind(attempt, now(), invite.id).run()
  if (!attemptResult.success || attemptResult.meta.changes !== 1) {
    return { ok: false, response: jsonResponse(503, 'delivery_state_not_persisted', 'Invite delivery could not be started safely. Retry the same invite.', { retryable: true }) }
  }
  try {
    await ensureKVRecord(env, invite)
  } catch (error) {
    await markDeliveryFailure(env, invite.id, safeError(error))
    return { ok: false, response: jsonResponse(502, 'invite_kv_sync_failed', 'Invite could not be synchronized to KV. Retry the same invite.', { retryable: true }) }
  }
  if (!forceSend && invite.status === 'sent' && invite.sent_at !== null) {
    const timestamp = now()
    const result = await env.WAITLIST_DB.prepare(
      `UPDATE waitlist_entries SET status = 'invited', updated_at = ?
        WHERE id = ? AND status IN ('approved', 'invited')`,
    ).bind(timestamp, invite.waitlist_entry_id).run()
    if (!result.success || result.meta.changes !== 1) return { ok: false, response: jsonResponse(503, 'delivery_state_not_persisted', 'Invite delivery state could not be reconciled. Retry the same invite.', { retryable: true }) }
    return { ok: true }
  }
  if (env.INVITE_SENDING_ENABLED !== 'true') {
    await markDeliveryFailure(env, invite.id, 'invite sending is disabled')
    return { ok: false, response: jsonResponse(503, 'email_delivery_disabled', 'Invite email delivery is disabled until explicitly enabled.', { retryable: false }) }
  }
  if (!env.EMAIL || !env.INVITE_FROM_EMAIL) {
    await markDeliveryFailure(env, invite.id, 'email binding or INVITE_FROM_EMAIL is not configured')
    return { ok: false, response: jsonResponse(503, 'email_not_configured', 'Invite delivery is not configured.', { retryable: true }) }
  }
  if (!env.APP_ORIGIN) {
    await markDeliveryFailure(env, invite.id, 'APP_ORIGIN is not configured')
    return { ok: false, response: jsonResponse(503, 'invite_origin_not_configured', 'Invite links are not configured.', { retryable: true }) }
  }
  const origin = env.APP_ORIGIN.replace(/\/$/, '')
  const redeemUrl = `${origin}/gate?invite=${encodeURIComponent(invite.code)}`
  const sendStartedAt = now()
  const sendState = await env.WAITLIST_DB.prepare(
    `UPDATE beta_invites
        SET status = 'sent', sent_at = COALESCE(sent_at, ?), delivery_error = NULL, updated_at = ?
      WHERE id = ? AND status IN ('issued', 'sent', 'delivery_failed')`,
  ).bind(sendStartedAt, sendStartedAt, invite.id).run()
  if (!sendState.success || sendState.meta.changes !== 1) {
    return { ok: false, response: jsonResponse(503, 'delivery_state_not_persisted', 'Invite delivery could not be started safely. Retry the same invite.', { retryable: true }) }
  }
  try {
    await env.EMAIL.send({
      to: email,
      from: { email: env.INVITE_FROM_EMAIL, name: 'Orbital Markets' },
      subject: 'Your Orbital Markets beta invitation',
      html: `<p>Welcome to the Orbital Markets closed beta.</p><p>Use the invitation code below to activate access:</p><p><strong>${invite.code}</strong></p><p><a href="${redeemUrl}">Open Orbital Markets</a></p><p>This invitation is intended for you and can be used in one browser.</p>`,
      text: `Welcome to the Orbital Markets closed beta.\n\nYour invitation code: ${invite.code}\n\nOpen ${redeemUrl} to activate access. This invitation is intended for you and can be used in one browser.`,
    })
  } catch (error) {
    await markDeliveryFailure(env, invite.id, safeError(error))
    return { ok: false, response: jsonResponse(502, 'email_delivery_failed', 'Invite email was not accepted by the provider. Retry the same invite.', { retryable: true }) }
  }
  const timestamp = now()
  const result = await env.WAITLIST_DB.prepare(
    `UPDATE waitlist_entries SET status = 'invited', updated_at = ?
      WHERE id = ? AND status IN ('approved', 'invited')`,
  ).bind(timestamp, invite.waitlist_entry_id).run()
  if (!result.success || result.meta.changes !== 1) {
    return { ok: false, response: jsonResponse(503, 'delivery_state_not_persisted', 'Email was accepted but delivery state could not be persisted. Retry the same invite.', { retryable: true }) }
  }
  return { ok: true }
}

async function ensureKVRecord(env: Env, invite: InviteRow): Promise<void> {
  const key = `${CODE_KEY_PREFIX}${invite.code}`
  const raw = await env.BETA_INVITES.get(key)
  let record: Record<string, unknown> = { waitlist_entry_id: invite.waitlist_entry_id, created_at: invite.created_at }
  if (raw) {
    try {
      const parsed: unknown = JSON.parse(raw)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) record = parsed as Record<string, unknown>
    } catch {
      throw new Error('corrupt KV invite record')
    }
  }
  record.waitlist_entry_id = invite.waitlist_entry_id
  record.created_at = invite.created_at
  await env.BETA_INVITES.put(key, JSON.stringify(record))
}

async function activeInvite(env: Env, entryId: string): Promise<InviteRow | null> {
  return await env.WAITLIST_DB.prepare(
    `SELECT id, waitlist_entry_id, code, status, bound_cookie_id, created_at, sent_at, redeemed_at, revoked_at,
            delivery_attempts, delivery_error, updated_at
       FROM beta_invites WHERE waitlist_entry_id = ? AND status IN ('issued', 'sent', 'delivery_failed')
       ORDER BY created_at DESC LIMIT 1`,
  ).bind(entryId).first<InviteRow>()
}

async function findInvite(env: Env, id: string): Promise<InviteRow | null> {
  return await env.WAITLIST_DB.prepare(
    `SELECT id, waitlist_entry_id, code, status, bound_cookie_id, created_at, sent_at, redeemed_at, revoked_at,
            delivery_attempts, delivery_error, updated_at
       FROM beta_invites WHERE id = ?`,
  ).bind(id).first<InviteRow>()
}

async function inviteResult(env: Env, id: string, idempotent: boolean): Promise<Response> {
  const invite = await findInvite(env, id)
  if (!invite) return jsonResponse(404, 'invite_not_found', 'Invite not found.')
  return jsonOk({ invite: { ...invite, idempotent } })
}

async function markDeliveryFailure(env: Env, inviteId: string, error: string): Promise<void> {
  await env.WAITLIST_DB.prepare(
    `UPDATE beta_invites SET status = 'delivery_failed', delivery_error = ?, updated_at = ?
      WHERE id = ? AND status IN ('issued', 'sent', 'delivery_failed')`,
  ).bind(error.slice(0, 500), now(), inviteId).run()
}

async function recordDeliveryFailure(env: Env, actor: AccessIdentity, inviteId: string, entryId: string): Promise<void> {
  await env.WAITLIST_DB.batch([
    auditInsert(env, actor, 'invite.delivery_failed', 'beta_invite', inviteId, { waitlist_entry_id: entryId, retryable: true }, null, now()),
  ])
}

async function writeKVRevocation(env: Env, code: string, entryId: string, revokedAt: number): Promise<void> {
  const key = `${CODE_KEY_PREFIX}${code}`
  const raw = await env.BETA_INVITES.get(key)
  let record: Record<string, unknown> = { waitlist_entry_id: entryId, created_at: now() }
  if (raw) {
    try {
      const parsed: unknown = JSON.parse(raw)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) record = parsed as Record<string, unknown>
    } catch {
      throw new Error('corrupt KV invite record')
    }
  }
  record.waitlist_entry_id = entryId
  record.revoked_at = revokedAt
  await env.BETA_INVITES.put(key, JSON.stringify(record))
}

function generateCode(): string {
  const bytes = new Uint8Array(CODE_LENGTH)
  const limit = 256 - (256 % CODE_ALPHABET.length)
  let code = ''
  while (code.length < CODE_LENGTH) {
    crypto.getRandomValues(bytes)
    for (const byte of bytes) {
      if (byte >= limit) continue
      code += CODE_ALPHABET[byte % CODE_ALPHABET.length]
      if (code.length === CODE_LENGTH) break
    }
  }
  return code
}

function safeError(error: unknown): string {
  return error instanceof Error ? error.message.slice(0, 500) : String(error).slice(0, 500)
}

class InviteDeliveryError extends Error {
  constructor(readonly code: string, message: string, readonly status: number) {
    super(message)
  }
}

function now(): number {
  return Math.floor(Date.now() / 1_000)
}
