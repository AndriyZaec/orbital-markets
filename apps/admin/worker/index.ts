import { authenticateAccess } from './auth/access'
import { handleInviteRoute } from './features/invites/routes'
import { handleWaitlistRoute } from './features/waitlist/routes'
import { jsonResponse } from './shared/http'
import type { Env } from './types'

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const identity = await authenticateAccess(request, env)
    if (identity instanceof Response) return identity

    const url = new URL(request.url)
    if (url.pathname === '/api/admin/v1/me' && request.method === 'GET') {
      return new Response(JSON.stringify({ email: identity.email }), {
        status: 200,
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
      })
    }
    if (url.pathname.startsWith('/api/admin/')) {
      const path = url.pathname.replace('/api/admin/v1', '')
      if (url.pathname.startsWith('/api/admin/v1/')) {
        if (path === '/analytics' && request.method === 'GET') return await proxyAnalytics(env)
        if (path.startsWith('/invites/') || path.match(/^\/waitlist\/[^/]+\/invites$/)) {
          return await handleInviteRoute(request, env, identity, path)
}

async function proxyAnalytics(env: Env): Promise<Response> {
  if (!env.ANALYTICS_API_URL || !env.ANALYTICS_API_TOKEN) {
    return jsonResponse(503, 'analytics_not_configured', 'Live analytics is not configured.', { retryable: true })
  }
  try {
    const response = await fetch(`${env.ANALYTICS_API_URL.replace(/\/$/, '')}/api/v1/analytics`, {
      headers: { 'x-analytics-token': env.ANALYTICS_API_TOKEN },
    })
    const body = await response.text()
    return new Response(body, {
      status: response.status,
      headers: { 'content-type': response.headers.get('content-type') ?? 'application/json', 'cache-control': 'no-store' },
    })
  } catch {
    return jsonResponse(502, 'analytics_upstream_unavailable', 'Live analytics is temporarily unavailable.', { retryable: true })
  }
}
        return await handleWaitlistRoute(request, env, identity, path)
      }
      return jsonResponse(404, 'not_found', 'Admin route not found.')
    }

    return env.ASSETS.fetch(request)
  },
} satisfies ExportedHandler<Env>
