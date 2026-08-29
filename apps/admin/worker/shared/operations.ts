import type { AccessIdentity, Env } from '../types'
import { jsonResponse } from './http'

const COOLDOWN_SECONDS = 10

export function requestId(request: Request): string {
  return request.headers.get('x-request-id')?.trim() || crypto.randomUUID()
}

export async function acquireMutationCooldown(env: Env, actor: AccessIdentity, action: string): Promise<Response | null> {
  const key = `admin-cooldown:${actor.email}:${action}`
  const existing = await env.BETA_INVITES.get(key)
  if (existing) return jsonResponse(429, 'mutation_rate_limited', 'Please wait before repeating this mutation.', { retry_after_seconds: COOLDOWN_SECONDS })
  await env.BETA_INVITES.put(key, '1', { expirationTtl: COOLDOWN_SECONDS })
  return null
}

export function logMutation(request: Request, actor: AccessIdentity, action: string, targetType: string, targetId: string): void {
  console.log(JSON.stringify({
    level: 'info',
    message: 'admin mutation',
    request_id: requestId(request),
    actor: actor.email,
    action,
    target_type: targetType,
    target_id: targetId,
  }))
}

export function withRequestId(response: Response, id: string): Response {
  const headers = new Headers(response.headers)
  headers.set('x-request-id', id)
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers })
}
