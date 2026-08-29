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
        if (path.startsWith('/invites/') || path.match(/^\/waitlist\/[^/]+\/invites$/)) {
          return await handleInviteRoute(request, env, identity, path)
        }
        return await handleWaitlistRoute(request, env, identity, path)
      }
      return jsonResponse(404, 'not_found', 'Admin route not found.')
    }

    return env.ASSETS.fetch(request)
  },
} satisfies ExportedHandler<Env>
