export function jsonResponse(status: number, error: string, message: string, details?: object): Response {
  return new Response(JSON.stringify({ error, message, ...(details ?? {}) }), {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
  })
}

export function jsonOk(body: object, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
  })
}

export async function readJson(request: Request): Promise<Record<string, unknown> | null> {
  const contentLength = request.headers.get('content-length')
  if (contentLength && Number(contentLength) > 32_768) return null
  try {
    const body: unknown = await request.json()
    if (!body || typeof body !== 'object' || Array.isArray(body)) return null
    return body as Record<string, unknown>
  } catch {
    return null
  }
}

export function requireIdempotencyKey(request: Request): string | Response {
  const key = request.headers.get('Idempotency-Key')?.trim() ?? ''
  if (key.length < 8 || key.length > 200) {
    return jsonResponse(400, 'idempotency_key_required', 'A valid Idempotency-Key header is required.')
  }
  return key
}

export function parseLimit(value: string | null, defaultValue = 50, max = 100): number {
  const parsed = value ? Number(value) : defaultValue
  if (!Number.isInteger(parsed) || parsed < 1) return defaultValue
  return Math.min(parsed, max)
}

export function encodeCursor(value: object): string {
  return b64urlEncode(JSON.stringify(value))
}

export function decodeCursor<T>(value: string | null): T | null {
  if (!value) return null
  try {
    return JSON.parse(new TextDecoder().decode(b64urlDecode(value))) as T
  } catch {
    return null
  }
}

function b64urlEncode(value: string): string {
  return btoa(value).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function b64urlDecode(value: string): Uint8Array {
  const padded = value.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((value.length + 3) % 4)
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index)
  return bytes
}
