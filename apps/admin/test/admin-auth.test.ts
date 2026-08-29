import { afterEach, describe, expect, it, vi } from 'vitest'
import worker from '../worker/index'
import type { Env } from '../worker/types'

const adminToken = 'test-admin-token'

afterEach(() => vi.unstubAllGlobals())

function testEnv(): Env {
  return {
    ASSETS: { fetch: async () => new Response('asset') } as Fetcher,
    BETA_INVITES: {} as KVNamespace,
    WAITLIST_DB: {} as D1Database,
    ADMIN_TOKEN: adminToken,
  }
}

describe('admin token boundary', () => {
  it('serves the login shell without exposing admin data', async () => {
    const response = await worker.fetch(new Request('https://admin.orbitalmarkets.xyz/'), testEnv())
    expect(response.status).toBe(200)
    expect(await response.text()).toBe('asset')
  })

  it('rejects API requests without an admin token', async () => {
    const response = await worker.fetch(new Request('https://admin.orbitalmarkets.xyz/api/admin/v1/me'), testEnv())
    expect(response.status).toBe(401)
    expect(await response.json()).toEqual({
      error: 'admin_authentication_required',
      message: 'An admin token is required.',
    })
    expect(response.headers.get('cache-control')).toBe('no-store')
    expect(response.headers.get('x-request-id')).toBeTruthy()
  })

  it('accepts the configured bearer token and returns the operator identity', async () => {
    const response = await worker.fetch(new Request('https://admin.orbitalmarkets.xyz/api/admin/v1/me', {
      headers: { Authorization: `Bearer ${adminToken}` },
    }), testEnv())
    expect(response.status).toBe(200)
    expect(await response.json()).toEqual({ email: 'admin' })
  })

  it('rejects an invalid bearer token', async () => {
    const response = await worker.fetch(new Request('https://admin.orbitalmarkets.xyz/api/admin/v1/me', {
      headers: { Authorization: 'Bearer wrong-token' },
    }), testEnv())
    expect(response.status).toBe(403)
    expect(await response.json()).toEqual({
      error: 'admin_token_invalid',
      message: 'The admin token is invalid.',
    })
  })
})
