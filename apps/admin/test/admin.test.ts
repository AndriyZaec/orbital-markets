import { env } from 'cloudflare:workers'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { handleInviteRoute } from '../worker/features/invites/routes'
import { handleWaitlistRoute } from '../worker/features/waitlist/routes'
import type { Env } from '../worker/types'

const actor = { email: 'operator@example.com' }
const failedDeliveryActor = { email: 'delivery-operator@example.com' }
const testEnv = env as unknown as Env

beforeEach(async () => {
  await env.WAITLIST_DB.batch([
    env.WAITLIST_DB.prepare('DELETE FROM admin_actions'),
    env.WAITLIST_DB.prepare('DELETE FROM beta_invites'),
    env.WAITLIST_DB.prepare('DELETE FROM waitlist_entries'),
  ])
  vi.restoreAllMocks()
})

async function addEntry(id: string, status = 'pending'): Promise<void> {
  const timestamp = Math.floor(Date.now() / 1_000)
  await env.WAITLIST_DB.prepare(
    `INSERT INTO waitlist_entries
      (id, email, profile, monthly_volume, source, status, created_at, updated_at)
     VALUES (?, ?, 'active_trader', '100k_1m', 'landing', ?, ?, ?)`,
  ).bind(id, `${id}@example.com`, status, timestamp, timestamp).run()
}

function request(path: string, init: RequestInit = {}): Request {
  return new Request(`https://admin.orbitalmarkets.xyz/api/admin/v1${path}`, init)
}

describe('waitlist admin operations', () => {
  it('lists filtered entries and approves with an audited idempotent transition', async () => {
    await addEntry('pending-one')
    await addEntry('approved-one', 'approved')
    const list = await handleWaitlistRoute(request('/waitlist?status=pending'), testEnv, actor, '/waitlist')
    expect((await list.json()).items).toHaveLength(1)

    const init = { method: 'POST', headers: { 'Idempotency-Key': 'approve-key-1' } }
    const response = await handleWaitlistRoute(request('/waitlist/pending-one/approve', init), testEnv, actor, '/waitlist/pending-one/approve')
    expect(response.status).toBe(200)
    const retry = await handleWaitlistRoute(request('/waitlist/pending-one/approve', init), testEnv, actor, '/waitlist/pending-one/approve')
    expect(await retry.json()).toMatchObject({ ok: true, idempotent: true, status: 'approved' })
    expect(await env.WAITLIST_DB.prepare('SELECT status FROM waitlist_entries WHERE id = ?').bind('pending-one').first()).toEqual({ status: 'approved' })
    expect(await env.WAITLIST_DB.prepare('SELECT COUNT(*) AS count FROM admin_actions').first()).toEqual({ count: 1 })
  })

  it('rejects transitions from a non-pending state and bulk-mutation partial selections', async () => {
    await addEntry('already-approved', 'approved')
    const response = await handleWaitlistRoute(request('/waitlist/already-approved/reject', { method: 'POST', headers: { 'Idempotency-Key': 'reject-key-1' } }), testEnv, actor, '/waitlist/already-approved/reject')
    expect(response.status).toBe(409)
    expect(await response.json()).toMatchObject({ error: 'invalid_state' })

    const bulk = await handleWaitlistRoute(request('/waitlist/bulk-approve', { method: 'POST', headers: { 'Idempotency-Key': 'bulk-key-1', 'content-type': 'application/json' }, body: JSON.stringify({ ids: ['missing'] }) }), testEnv, actor, '/waitlist/bulk-approve')
    expect(bulk.status).toBe(409)
  })
})

describe('invite lifecycle', () => {
  it('writes the same invite to D1 and KV, sends both email formats, then disables it', async () => {
    await addEntry('invite-owner', 'approved')
    const send = vi.fn(async () => ({ messageId: 'message-1' }))
    const inviteEnv = { ...testEnv, EMAIL: { send }, INVITE_FROM_EMAIL: 'beta@orbitalmarkets.xyz', APP_ORIGIN: 'https://app.orbitalmarkets.xyz', INVITE_SENDING_ENABLED: 'true' } as Env
    const issue = await handleInviteRoute(request('/waitlist/invite-owner/invites', { method: 'POST', headers: { 'Idempotency-Key': 'issue-key-1' } }), inviteEnv, actor, '/waitlist/invite-owner/invites')
    expect(issue.status).toBe(200)
    const issued = (await issue.json()).invite as { id: string; code: string; status: string }
    expect(issued.status).toBe('sent')
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ text: expect.stringContaining(issued.code), html: expect.stringContaining(issued.code) }))
    expect(await env.BETA_INVITES.get(`invite:${issued.code}`)).toContain('invite-owner')

    const revoke = await handleInviteRoute(request(`/invites/${issued.id}/revoke`, { method: 'POST', headers: { 'Idempotency-Key': 'revoke-key-1' } }), inviteEnv, actor, `/invites/${issued.id}/revoke`)
    expect(revoke.status).toBe(200)
    expect(await env.BETA_INVITES.get(`invite:${issued.code}`)).toContain('revoked_at')
    expect(await env.WAITLIST_DB.prepare('SELECT status FROM beta_invites WHERE id = ?').bind(issued.id).first()).toEqual({ status: 'revoked' })
  })

  it('preserves a delivery failure for a safe resend', async () => {
    await addEntry('failed-owner', 'approved')
    const send = vi.fn().mockRejectedValueOnce(new Error('provider rejected message')).mockResolvedValue({ messageId: 'message-2' })
    const inviteEnv = { ...testEnv, EMAIL: { send }, INVITE_FROM_EMAIL: 'beta@orbitalmarkets.xyz', APP_ORIGIN: 'https://app.orbitalmarkets.xyz', INVITE_SENDING_ENABLED: 'true' } as Env
    const first = await handleInviteRoute(request('/waitlist/failed-owner/invites', { method: 'POST', headers: { 'Idempotency-Key': 'issue-key-2' } }), inviteEnv, failedDeliveryActor, '/waitlist/failed-owner/invites')
    expect(first.status).toBe(502)
    const invite = await env.WAITLIST_DB.prepare('SELECT id, status, delivery_attempts FROM beta_invites').first<{ id: string; status: string; delivery_attempts: number }>()
    expect(invite).toMatchObject({ status: 'delivery_failed', delivery_attempts: 1 })

    const retry = await handleInviteRoute(request(`/invites/${invite!.id}/resend`, { method: 'POST', headers: { 'Idempotency-Key': 'resend-key-2' } }), inviteEnv, failedDeliveryActor, `/invites/${invite!.id}/resend`)
    expect(retry.status).toBe(200)
    expect(await env.WAITLIST_DB.prepare('SELECT status, delivery_attempts FROM beta_invites').first()).toEqual({ status: 'sent', delivery_attempts: 2 })
  })

  it('does not let concurrent resends overwrite an active delivery claim', async () => {
    await addEntry('concurrent-owner', 'approved')
    let release!: () => void
    const waiting = new Promise<void>((resolve) => { release = resolve })
    const send = vi.fn(async () => { await waiting; return { messageId: 'message-concurrent' } })
    const inviteEnv = { ...testEnv, EMAIL: { send }, INVITE_FROM_EMAIL: 'beta@orbitalmarkets.xyz', APP_ORIGIN: 'https://app.orbitalmarkets.xyz', INVITE_SENDING_ENABLED: 'true' } as Env
    const firstPromise = handleInviteRoute(request('/waitlist/concurrent-owner/invites', { method: 'POST', headers: { 'Idempotency-Key': 'issue-key-3' } }), inviteEnv, actor, '/waitlist/concurrent-owner/invites')
    await new Promise((resolve) => setTimeout(resolve, 0))
    const invite = await env.WAITLIST_DB.prepare('SELECT id FROM beta_invites').first<{ id: string }>()
    const secondPromise = handleInviteRoute(request(`/invites/${invite!.id}/resend`, { method: 'POST', headers: { 'Idempotency-Key': 'resend-key-3' } }), inviteEnv, actor, `/invites/${invite!.id}/resend`)
    const second = await secondPromise
    expect(second.status).toBe(409)
    release()
    expect((await firstPromise).status).toBe(200)
    expect(await env.WAITLIST_DB.prepare('SELECT status FROM beta_invites').first()).toEqual({ status: 'sent' })
    expect(send).toHaveBeenCalledTimes(1)
  })
})
