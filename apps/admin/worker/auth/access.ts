import { createRemoteJWKSet, jwtVerify } from 'jose'
import type { AccessIdentity, Env } from '../types'
import { jsonResponse } from '../shared/http'

export async function authenticateAccess(request: Request, env: Env): Promise<AccessIdentity | Response> {
  const token = request.headers.get('Cf-Access-Jwt-Assertion')
  if (!token) return jsonResponse(401, 'access_authentication_required', 'Cloudflare Access authentication required.')
  if (!env.TEAM_DOMAIN || !env.POLICY_AUD) return jsonResponse(500, 'access_configuration_missing', 'Cloudflare Access validation is not configured.')

  try {
    const teamDomain = env.TEAM_DOMAIN.replace(/\/$/, '')
    const jwks = createRemoteJWKSet(new URL(`${teamDomain}/cdn-cgi/access/certs`))
    const { payload } = await jwtVerify(token, jwks, {
      issuer: teamDomain,
      audience: env.POLICY_AUD,
    })
    if (typeof payload.email !== 'string' || payload.email.trim() === '') {
      return jsonResponse(403, 'access_identity_missing', 'Cloudflare Access identity has no email.')
    }
    return { email: payload.email.trim().toLowerCase() }
  } catch {
    return jsonResponse(403, 'access_assertion_invalid', 'Cloudflare Access assertion is invalid.')
  }
}
