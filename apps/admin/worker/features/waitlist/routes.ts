import type { AccessIdentity, Env } from '../../types'
import { auditInsert, findIdempotentAction } from '../audit/service'
import {
  decodeCursor,
  encodeCursor,
  jsonOk,
  jsonResponse,
  parseLimit,
  readJson,
  requireIdempotencyKey,
} from '../../shared/http'
import { logMutation } from '../../shared/operations'

const STATUSES = new Set(['pending', 'approved', 'rejected', 'invited'])
const PROFILES = new Set(['active_trader', 'trading_team', 'researching'])
const VOLUMES = new Set(['under_10k', '10k_50k', '50k_100k', '100k_1m', '1m_10m', '10m_plus'])

interface WaitlistEntry {
  id: string
  email: string
  profile: string
  monthly_volume: string
  source: string
  status: string
  created_at: number
  updated_at: number
}

interface WaitlistCursor {
  created_at: number
  id: string
}

export async function handleWaitlistRoute(request: Request, env: Env, actor: AccessIdentity, path: string): Promise<Response> {
  if (request.method === 'GET' && path === '/waitlist') return listWaitlist(request, env)
  if (request.method === 'GET' && /^\/waitlist\/[^/]+$/.test(path)) return detailWaitlist(env, path.split('/')[2])
  if (request.method === 'GET' && /^\/users\/[^/]+$/.test(path)) return detailWaitlist(env, path.split('/')[2])
  if (request.method === 'GET' && path === '/audit') return listAudit(request, env)

  if (request.method === 'POST' && path === '/waitlist/bulk-approve') return bulkTransition(request, env, actor, 'approved')
  if (request.method === 'POST' && path === '/waitlist/bulk-reject') return bulkTransition(request, env, actor, 'rejected')
  const transition = transitionForPath(path)
  if (request.method === 'POST' && transition) return transitionWaitlist(request, env, actor, transition)
  return jsonResponse(404, 'not_found', 'Admin route not found.')
}

async function listWaitlist(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url)
  const limit = parseLimit(url.searchParams.get('limit'))
  const status = url.searchParams.get('status')
  const profile = url.searchParams.get('profile')
  const volume = url.searchParams.get('monthly_volume')
  const query = url.searchParams.get('q')?.trim().toLowerCase() ?? ''
  if ((status && !STATUSES.has(status)) || (profile && !PROFILES.has(profile)) || (volume && !VOLUMES.has(volume))) {
    return jsonResponse(400, 'invalid_filter', 'One or more waitlist filters are invalid.')
  }
  const from = parseDateParam(url.searchParams.get('from'))
  const to = parseDateParam(url.searchParams.get('to'))
  if ((url.searchParams.has('from') && from === null) || (url.searchParams.has('to') && to === null)) {
    return jsonResponse(400, 'invalid_date_filter', 'Date filters must be ISO dates or Unix seconds.')
  }

  const cursor = decodeCursor<WaitlistCursor>(url.searchParams.get('cursor'))
  if (url.searchParams.get('cursor') && (!cursor || !Number.isInteger(cursor.created_at) || !cursor.id)) {
    return jsonResponse(400, 'invalid_cursor', 'The cursor is invalid or expired.')
  }
  const conditions: string[] = []
  const values: unknown[] = []
  if (status) { conditions.push('status = ?'); values.push(status) }
  if (profile) { conditions.push('profile = ?'); values.push(profile) }
  if (volume) { conditions.push('monthly_volume = ?'); values.push(volume) }
  if (query) { conditions.push('lower(email) LIKE ?'); values.push(`%${query}%`) }
  if (from !== null) { conditions.push('created_at >= ?'); values.push(from) }
  if (to !== null) { conditions.push('created_at < ?'); values.push(to + 86_400) }
  if (cursor) {
    conditions.push('(created_at < ? OR (created_at = ? AND id < ?))')
    values.push(cursor.created_at, cursor.created_at, cursor.id)
  }
  const where = conditions.length > 0 ? `WHERE ${conditions.join(' AND ')}` : ''
  const rows = await env.WAITLIST_DB.prepare(
    `SELECT id, email, profile, monthly_volume, source, status, created_at, updated_at
       FROM waitlist_entries ${where}
      ORDER BY created_at DESC, id DESC LIMIT ?`,
  ).bind(...values, limit + 1).all<WaitlistEntry>()
  const items = rows.results.slice(0, limit)
  const last = items.at(-1)
  const nextCursor = rows.results.length > limit && last
    ? encodeCursor({ created_at: last.created_at, id: last.id })
    : null
  return jsonOk({ items, next_cursor: nextCursor })
}

async function detailWaitlist(env: Env, id: string): Promise<Response> {
  const entry = await env.WAITLIST_DB.prepare(
    'SELECT id, email, profile, monthly_volume, source, status, created_at, updated_at FROM waitlist_entries WHERE id = ?',
  ).bind(id).first<WaitlistEntry>()
  if (!entry) return jsonResponse(404, 'waitlist_entry_not_found', 'Waitlist entry not found.')
  const invites = await env.WAITLIST_DB.prepare(
    `SELECT id, waitlist_entry_id, code, status, bound_cookie_id, created_at, sent_at, redeemed_at, revoked_at,
            delivery_attempts, delivery_error, updated_at
       FROM beta_invites WHERE waitlist_entry_id = ? ORDER BY created_at DESC`,
  ).bind(id).all()
  const audit = await env.WAITLIST_DB.prepare(
    `SELECT id, actor_email, action, target_type, target_id, idempotency_key, metadata_json, created_at
       FROM admin_actions
      WHERE (target_type = 'waitlist_entry' AND target_id = ?)
         OR (target_type = 'beta_invite' AND target_id IN (SELECT id FROM beta_invites WHERE waitlist_entry_id = ?))
      ORDER BY created_at DESC LIMIT 100`,
  ).bind(id, id).all()
  return jsonOk({ entry, invites: invites.results, audit: audit.results })
}

async function listAudit(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url)
  const limit = parseLimit(url.searchParams.get('limit'))
  const cursor = decodeCursor<{ created_at: number; id: string }>(url.searchParams.get('cursor'))
  if (url.searchParams.get('cursor') && (!cursor || !Number.isInteger(cursor.created_at) || !cursor.id)) {
    return jsonResponse(400, 'invalid_cursor', 'The cursor is invalid or expired.')
  }
  const action = url.searchParams.get('action')?.trim() ?? ''
  const conditions = action ? ['action = ?'] : []
  const values: unknown[] = action ? [action] : []
  if (cursor) {
    conditions.push('(created_at < ? OR (created_at = ? AND id < ?))')
    values.push(cursor.created_at, cursor.created_at, cursor.id)
  }
  const where = conditions.length > 0 ? `WHERE ${conditions.join(' AND ')}` : ''
  const rows = await env.WAITLIST_DB.prepare(
    `SELECT id, actor_email, action, target_type, target_id, idempotency_key, metadata_json, created_at
       FROM admin_actions ${where} ORDER BY created_at DESC, id DESC LIMIT ?`,
  ).bind(...values, limit + 1).all()
  const items = rows.results.slice(0, limit)
  const last = items.at(-1) as { created_at: number; id: string } | undefined
  const nextCursor = rows.results.length > limit && last
    ? encodeCursor({ created_at: last.created_at, id: last.id })
    : null
  return jsonOk({ items, next_cursor: nextCursor })
}

function transitionForPath(path: string): 'approved' | 'rejected' | null {
  const match = path.match(/^\/waitlist\/([^/]+)\/(approve|reject)$/)
  if (!match) return null
  return match[2] === 'approve' ? 'approved' : 'rejected'
}

async function transitionWaitlist(request: Request, env: Env, actor: AccessIdentity, transition: 'approved' | 'rejected'): Promise<Response> {
  const id = new URL(request.url).pathname.split('/')[5]
  const keyOrResponse = requireIdempotencyKey(request)
  if (keyOrResponse instanceof Response) return keyOrResponse
  const previous = await findIdempotentAction(env, actor, keyOrResponse)
  if (previous) {
    if (previous.action !== `waitlist.${transition}` || previous.target_id !== id) return jsonResponse(409, 'idempotency_key_reused', 'The Idempotency-Key was already used for another action.')
    return jsonOk({ ok: true, idempotent: true, status: transition })
  }

  const entry = await env.WAITLIST_DB.prepare('SELECT id, status FROM waitlist_entries WHERE id = ?').bind(id).first<{ id: string; status: string }>()
  if (!entry) return jsonResponse(404, 'waitlist_entry_not_found', 'Waitlist entry not found.')
  if (entry.status !== 'pending') return jsonResponse(409, 'invalid_state', `Cannot mark a ${entry.status} entry as ${transition}.`)
  const timestamp = now()
  const batch = await env.WAITLIST_DB.batch([
    env.WAITLIST_DB.prepare('UPDATE waitlist_entries SET status = ?, updated_at = ? WHERE id = ? AND status = \'pending\'').bind(transition, timestamp, id),
    auditInsert(env, actor, `waitlist.${transition}`, 'waitlist_entry', id, { from: 'pending', to: transition }, keyOrResponse, timestamp),
  ])
  if (batch[0].meta.changes !== 1) return jsonResponse(409, 'invalid_state', 'The waitlist entry changed before this action was applied.')
  logMutation(request, actor, `waitlist.${transition}`, 'waitlist_entry', id)
  return jsonOk({ ok: true, status: transition })
}

async function bulkTransition(request: Request, env: Env, actor: AccessIdentity, transition: 'approved' | 'rejected'): Promise<Response> {
  const keyOrResponse = requireIdempotencyKey(request)
  if (keyOrResponse instanceof Response) return keyOrResponse
  const previous = await findIdempotentAction(env, actor, keyOrResponse)
  if (previous) {
    if (previous.action !== `waitlist.bulk_${transition}` || previous.target_id !== keyOrResponse) return jsonResponse(409, 'idempotency_key_reused', 'The Idempotency-Key was already used for another action.')
    const metadata = JSON.parse(previous.metadata_json) as { ids?: unknown }
    return jsonOk({ ok: true, idempotent: true, status: transition, updated_ids: metadata.ids ?? [] })
  }
  const body = await readJson(request)
  const ids = Array.isArray(body?.ids) ? body.ids.filter((id): id is string => typeof id === 'string' && id.length > 0) : []
  const uniqueIds = [...new Set(ids)]
  if (uniqueIds.length < 1 || uniqueIds.length > 100) return jsonResponse(400, 'invalid_selection', 'Select between 1 and 100 waitlist entries.')
  const placeholders = uniqueIds.map(() => '?').join(', ')
  const pending = await env.WAITLIST_DB.prepare(`SELECT id FROM waitlist_entries WHERE status = 'pending' AND id IN (${placeholders})`).bind(...uniqueIds).all<{ id: string }>()
  if (pending.results.length !== uniqueIds.length) return jsonResponse(409, 'invalid_state', 'All selected entries must still be pending.')
  const timestamp = now()
  const result = await env.WAITLIST_DB.batch([
    env.WAITLIST_DB.prepare(`UPDATE waitlist_entries SET status = ?, updated_at = ? WHERE status = 'pending' AND id IN (${placeholders})`).bind(transition, timestamp, ...uniqueIds),
    auditInsert(env, actor, `waitlist.bulk_${transition}`, 'waitlist_batch', keyOrResponse, { ids: uniqueIds, to: transition }, keyOrResponse, timestamp),
  ])
  if (result[0].meta.changes !== uniqueIds.length) return jsonResponse(409, 'invalid_state', 'The selection changed before this action was applied.')
  logMutation(request, actor, `waitlist.bulk_${transition}`, 'waitlist_batch', keyOrResponse)
  return jsonOk({ ok: true, status: transition, updated_ids: uniqueIds })
}

function parseDateParam(value: string | null): number | null {
  if (!value) return null
  if (/^\d+$/.test(value)) return Number(value)
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? null : Math.floor(parsed / 1_000)
}

function now(): number {
  return Math.floor(Date.now() / 1_000)
}
