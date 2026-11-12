import { env } from 'cloudflare:workers';
import { afterEach, expect, it, vi } from 'vitest';
import worker from '../src/index';

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

it('keeps the production cookie name by default', async () => {
  await env.BETA_INVITES.put('invite:DEFAULTCOOKIE', JSON.stringify({ created_at: Date.now() }));
  const response = await worker.fetch(new Request('https://app.example.com/gate/redeem', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: 'DEFAULTCOOKIE' }),
  }), env);
  expect(response.status).toBe(200);
  expect(response.headers.get('set-cookie')).toMatch(/^__beta=/);
});

it('redeems, reuses and refreshes only its isolated staging cookie', async () => {
  const staging = { ...env, BETA_COOKIE_NAME: '__beta_staging', COOKIE_DOMAIN: '.staging.example.com', JWT_SECRET: 'staging-only-secret' };
  const origin = 'https://staging.example.com';
  await env.BETA_INVITES.put('invite:STAGINGCOOKIE', JSON.stringify({ created_at: Date.now() }));
  const redeem = (cookie = '') => worker.fetch(new Request(origin + '/gate/redeem', {
    method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: cookie }, body: JSON.stringify({ code: 'STAGINGCOOKIE' }),
  }), staging);
  const first = await redeem();
  expect(first.status).toBe(200);
  const setCookie = first.headers.get('set-cookie')!;
  expect(setCookie).toMatch(/^__beta_staging=/);
  expect(setCookie).toContain('Domain=.staging.example.com');
  expect(setCookie).toContain('HttpOnly');
  expect(setCookie).toContain('Secure');
  const cookie = setCookie.split(';')[0];
  const value = cookie.slice(cookie.indexOf('=') + 1);
  expect((await redeem('__beta=' + value)).status).toBe(404);
  expect((await redeem('__beta=production-session; ' + cookie)).status).toBe(200);

  const rejected = await worker.fetch(new Request(origin + '/', { headers: { Cookie: '__beta=' + value, Accept: 'text/html' } }), staging);
  expect(rejected.status).toBe(302);
  expect(rejected.headers.get('location')).toBe(origin + '/gate');

  vi.stubGlobal('fetch', vi.fn(async () => new Response('staging origin')));
  vi.useFakeTimers();
  vi.setSystemTime(Date.now() + 24 * 24 * 60 * 60 * 1000);
  const refreshed = await worker.fetch(new Request(origin + '/', { headers: { Cookie: '__beta=production-session; ' + cookie } }), staging);
  expect(refreshed.status).toBe(200);
  expect(refreshed.headers.get('set-cookie')).toMatch(/^__beta_staging=/);
});

it('gates the standalone staging API and proxies authenticated bodies without buffering responses', async () => {
  const staging = { ...env, BETA_COOKIE_NAME: '__beta_staging', API_ORIGIN: 'https://isolated-staging.fly.dev' };
  await env.BETA_INVITES.put('invite:PROXYSTAGING', JSON.stringify({ created_at: Date.now() }));
  const redeemed = await worker.fetch(new Request('https://staging.example.com/gate/redeem', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: 'PROXYSTAGING' }),
  }), staging);
  const cookie = redeemed.headers.get('set-cookie')!.split(';')[0];
  const upstream = vi.fn(async (request: Request) => {
    expect(request.url).toBe('https://isolated-staging.fly.dev/api/v1/live/prepare?test=1');
    expect(request.method).toBe('POST');
    expect(request.headers.get('cookie')).toBe(cookie);
    expect(await request.text()).toBe('{"accounts":{"phoenix":"test-owner"}}');
    return new Response('data: progress\n\n', { headers: { 'Content-Type': 'text/event-stream' } });
  });
  vi.stubGlobal('fetch', upstream);
  const path = 'https://staging.example.com/api/v1/live/prepare?test=1';
  expect((await worker.fetch(new Request(path, { method: 'POST' }), staging)).status).toBe(404);
  expect(upstream).not.toHaveBeenCalled();
  const response = await worker.fetch(new Request(path, {
    method: 'POST', headers: { Cookie: cookie, 'Content-Type': 'application/json' },
    body: '{"accounts":{"phoenix":"test-owner"}}',
  }), staging);
  expect(response.status).toBe(200);
  expect(response.headers.get('content-type')).toBe('text/event-stream');
  expect(await response.text()).toBe('data: progress\n\n');
  expect(upstream).toHaveBeenCalledTimes(1);
});
