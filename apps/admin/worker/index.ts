import { authenticateAccess } from './auth/access'
import { handleInviteRoute } from './features/invites/routes'
import { handleWaitlistRoute } from './features/waitlist/routes'
import { jsonResponse } from './shared/http'
import { requestId, withRequestId } from './shared/operations'
import type { Env } from './types'

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const id = requestId(request)
    try {
      const identity = await authenticateAccess(request, env)
      if (identity instanceof Response) return withRequestId(identity, id)

      const url = new URL(request.url)
      if (url.pathname === '/api/admin/v1/me' && request.method === 'GET') {
        return withRequestId(new Response(JSON.stringify({ email: identity.email }), {
          status: 200,
          headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
        }), id)
      }
      if (url.pathname.startsWith('/api/admin/')) {
        const path = url.pathname.replace('/api/admin/v1', '')
        if (url.pathname.startsWith('/api/admin/v1/')) {
          let response: Response
          if (path === '/analytics' && request.method === 'GET') response = await proxyAnalytics(env)
          else if (path.startsWith('/invites/') || path.match(/^\/waitlist\/[^/]+\/invites$/)) {
            response = await handleInviteRoute(request, env, identity, path)
          } else response = await handleWaitlistRoute(request, env, identity, path)
          return withRequestId(response, id)
        }
        return withRequestId(jsonResponse(404, 'not_found', 'Admin route not found.'), id)
      }

      return withRequestId(await env.ASSETS.fetch(request), id)
    } catch (error) {
      console.log(JSON.stringify({
        level: 'error',
        message: 'admin request failed',
        request_id: id,
        error: error instanceof Error ? error.message : String(error),
      }))
      return withRequestId(jsonResponse(500, 'internal_error', 'The admin request failed.'), id)
    }
  },
} satisfies ExportedHandler<Env>

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
