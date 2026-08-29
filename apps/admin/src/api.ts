export interface WaitlistEntry {
  id: string
  email: string
  profile: string
  monthly_volume: string
  source: string
  status: 'pending' | 'approved' | 'rejected' | 'invited'
  created_at: number
  updated_at: number
}

export interface WaitlistDetail {
  entry: WaitlistEntry
  invites: InviteRecord[]
  audit: AuditRecord[]
}

export interface InviteRecord {
  id: string
  waitlist_entry_id: string
  code: string
  status: string
  bound_cookie_id: string | null
  created_at: number
  sent_at: number | null
  redeemed_at: number | null
  revoked_at: number | null
  delivery_attempts: number
  delivery_error: string | null
  updated_at: number
}

export interface AuditRecord {
  id: string
  actor_email: string
  action: string
  target_type: string
  target_id: string
  idempotency_key: string | null
  metadata_json: string
  created_at: number
}

interface ListResponse<T> {
  items: T[]
  next_cursor: string | null
}

export async function listWaitlist(params: URLSearchParams, signal?: AbortSignal): Promise<ListResponse<WaitlistEntry>> {
  return get<ListResponse<WaitlistEntry>>(`/api/admin/v1/waitlist?${params.toString()}`, signal)
}

export async function getWaitlistDetail(id: string, signal?: AbortSignal): Promise<WaitlistDetail> {
  return get<WaitlistDetail>(`/api/admin/v1/waitlist/${encodeURIComponent(id)}`, signal)
}

export async function transitionWaitlist(id: string, transition: 'approve' | 'reject'): Promise<void> {
  await mutate(`/api/admin/v1/waitlist/${encodeURIComponent(id)}/${transition}`)
}

export async function bulkTransition(ids: string[], transition: 'approve' | 'reject'): Promise<void> {
  await mutate(`/api/admin/v1/waitlist/bulk-${transition}`, { ids })
}

export async function listAudit(signal?: AbortSignal): Promise<ListResponse<AuditRecord>> {
  return get<ListResponse<AuditRecord>>('/api/admin/v1/audit?limit=100', signal)
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', signal })
  return parseResponse<T>(response)
}

async function mutate(path: string, body?: object): Promise<void> {
  const response = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'content-type': 'application/json', 'idempotency-key': crypto.randomUUID() },
    body: JSON.stringify(body ?? {}),
  })
  await parseResponse(response)
}

async function parseResponse<T>(response: Response): Promise<T> {
  const body: unknown = await response.json().catch(() => null)
  if (!response.ok) {
    const message = body && typeof body === 'object' && 'message' in body && typeof body.message === 'string'
      ? body.message
      : 'The admin request failed.'
    throw new Error(message)
  }
  return body as T
}
