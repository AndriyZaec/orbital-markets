import { createRemoteJWKSet, jwtVerify, type JWTPayload } from 'jose'

interface Env {
  ASSETS: Fetcher
  BETA_INVITES: KVNamespace
  WAITLIST_DB: D1Database
  EMAIL?: SendEmail
  TEAM_DOMAIN: string
  POLICY_AUD: string
}

interface AccessIdentity {
  email: string
  claims: JWTPayload
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const identity = await authenticateAccess(request, env)
    if (identity instanceof Response) return identity

    const url = new URL(request.url)
    if (url.pathname === '/api/admin/v1/me' && request.method === 'GET') {
      return jsonResponse(200, { email: identity.email })
    }
    if (url.pathname.startsWith('/api/admin/')) {
      return jsonResponse(404, { error: 'not_found', message: 'Admin route not found.' })
    }

    return env.ASSETS.fetch(request)
  },
} satisfies ExportedHandler<Env>

async function authenticateAccess(request: Request, env: Env): Promise<AccessIdentity | Response> {
  const token = request.headers.get('Cf-Access-Jwt-Assertion')
  if (!token) return jsonResponse(401, { error: 'access_authentication_required', message: 'Cloudflare Access authentication required.' })
  if (!env.TEAM_DOMAIN || !env.POLICY_AUD) return jsonResponse(500, { error: 'access_configuration_missing', message: 'Cloudflare Access validation is not configured.' })

  try {
    const teamDomain = env.TEAM_DOMAIN.replace(/\/$/, '')
    const jwks = createRemoteJWKSet(new URL(`${teamDomain}/cdn-cgi/access/certs`))
    const { payload } = await jwtVerify(token, jwks, {
      issuer: teamDomain,
      audience: env.POLICY_AUD,
    })
    if (typeof payload.email !== 'string' || payload.email.trim() === '') {
      return jsonResponse(403, { error: 'access_identity_missing', message: 'Cloudflare Access identity has no email.' })
    }
    return { email: payload.email.trim().toLowerCase(), claims: payload }
  } catch {
    return jsonResponse(403, { error: 'access_assertion_invalid', message: 'Cloudflare Access assertion is invalid.' })
  }
}

function jsonResponse(status: number, body: object): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
  })
}
