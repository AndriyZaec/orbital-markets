import type { AccessIdentity, Env } from '../types'
import { jsonResponse } from '../shared/http'

export async function authenticateAdminToken(request: Request, env: Env): Promise<AccessIdentity | Response> {
  if (!env.ADMIN_TOKEN) return jsonResponse(500, 'admin_token_not_configured', 'Admin token authentication is not configured.')

  const authorization = request.headers.get('Authorization') ?? ''
  const match = authorization.match(/^Bearer\s+(.+)$/i)
  if (!match) return jsonResponse(401, 'admin_authentication_required', 'An admin token is required.')
  if (!await tokensMatch(match[1].trim(), env.ADMIN_TOKEN)) {
    return jsonResponse(403, 'admin_token_invalid', 'The admin token is invalid.')
  }

  return { email: 'admin' }
}

async function tokensMatch(provided: string, expected: string): Promise<boolean> {
  const encoder = new TextEncoder()
  const [providedHash, expectedHash] = await Promise.all([
    crypto.subtle.digest('SHA-256', encoder.encode(provided)),
    crypto.subtle.digest('SHA-256', encoder.encode(expected)),
  ])
  const providedBytes = new Uint8Array(providedHash)
  const expectedBytes = new Uint8Array(expectedHash)
  let difference = providedBytes.length ^ expectedBytes.length
  for (let index = 0; index < providedBytes.length; index++) {
    difference |= providedBytes[index] ^ (expectedBytes[index] ?? 0)
  }
  return difference === 0
}
