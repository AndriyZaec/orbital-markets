import { afterEach, describe, expect, it, vi } from 'vitest'
import { generateKeyPair, exportJWK, SignJWT } from 'jose'
import worker from '../worker/index'
import type { Env } from '../worker/types'

const teamDomain = 'https://orbital.cloudflareaccess.com'
const audience = 'admin-audience'

afterEach(() => vi.unstubAllGlobals())

function testEnv(): Env {
  return {
    ASSETS: { fetch: async () => new Response('asset') } as Fetcher,
    BETA_INVITES: {} as KVNamespace,
    WAITLIST_DB: {} as D1Database,
    TEAM_DOMAIN: teamDomain,
    POLICY_AUD: audience,
  }
}

describe('admin Access boundary', () => {
  it('rejects requests without a Cloudflare Access assertion', async () => {
    const response = await worker.fetch(new Request('https://admin.orbitalmarkets.xyz/'), testEnv())
    expect(response.status).toBe(401)
    expect(await response.json()).toEqual({
      error: 'access_authentication_required',
      message: 'Cloudflare Access authentication required.',
    })
    expect(response.headers.get('cache-control')).toBe('no-store')
    expect(response.headers.get('x-request-id')).toBeTruthy()
  })

  it('validates issuer, audience, signature, and records the normalized operator email', async () => {
    const { publicKey, privateKey } = await generateKeyPair('RS256')
    const jwk = await exportJWK(publicKey)
    jwk.kid = 'current'
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ keys: [jwk] }), {
      headers: { 'content-type': 'application/json' },
    })))
    const token = await new SignJWT({ email: 'Operator@Example.com' })
      .setProtectedHeader({ alg: 'RS256', kid: 'current' })
      .setIssuer(teamDomain)
      .setAudience(audience)
      .setExpirationTime('5m')
      .sign(privateKey)

    const response = await worker.fetch(new Request('https://admin.orbitalmarkets.xyz/api/admin/v1/me', {
      headers: { 'Cf-Access-Jwt-Assertion': token },
    }), testEnv())
    expect(response.status).toBe(200)
    expect(await response.json()).toEqual({ email: 'operator@example.com' })

    const wrongAudience = await new SignJWT({ email: 'operator@example.com' })
      .setProtectedHeader({ alg: 'RS256', kid: 'current' })
      .setIssuer(teamDomain)
      .setAudience('another-application')
      .setExpirationTime('5m')
      .sign(privateKey)
    const denied = await worker.fetch(new Request('https://admin.orbitalmarkets.xyz/api/admin/v1/me', {
      headers: { 'Cf-Access-Jwt-Assertion': wrongAudience },
    }), testEnv())
    expect(denied.status).toBe(403)
  })

  it('rejects an invalid Access assertion without exposing verification details', async () => {
    const response = await worker.fetch(new Request('https://admin.orbitalmarkets.xyz/api/admin/v1/me', {
      headers: { 'Cf-Access-Jwt-Assertion': 'not-a-jwt' },
    }), testEnv())
    expect(response.status).toBe(403)
    expect(await response.json()).toEqual({
      error: 'access_assertion_invalid',
      message: 'Cloudflare Access assertion is invalid.',
    })
  })
})
