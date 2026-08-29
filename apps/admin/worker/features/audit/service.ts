import type { AccessIdentity, Env } from '../../types'

export interface AuditAction {
  id: string
  actor_email: string
  action: string
  target_type: string
  target_id: string
  idempotency_key: string | null
  metadata_json: string
  created_at: number
}

export async function findIdempotentAction(env: Env, actor: AccessIdentity, key: string): Promise<AuditAction | null> {
  return await env.WAITLIST_DB.prepare(
    'SELECT id, actor_email, action, target_type, target_id, idempotency_key, metadata_json, created_at FROM admin_actions WHERE actor_email = ? AND idempotency_key = ?',
  ).bind(actor.email, key).first<AuditAction>()
}

export function auditInsert(
  env: Env,
  actor: AccessIdentity,
  action: string,
  targetType: string,
  targetId: string,
  metadata: object,
  idempotencyKey: string | null,
  createdAt: number,
): D1PreparedStatement {
  return env.WAITLIST_DB.prepare(
    `INSERT INTO admin_actions
      (id, actor_email, action, target_type, target_id, idempotency_key, metadata_json, created_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
  ).bind(
    crypto.randomUUID(),
    actor.email,
    action,
    targetType,
    targetId,
    idempotencyKey,
    JSON.stringify(metadata),
    createdAt,
  )
}
