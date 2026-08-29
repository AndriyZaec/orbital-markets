import { env, exports } from 'cloudflare:workers';
import { afterEach, describe, expect, it, vi } from 'vitest';

const WAITLIST_URL = 'https://orbitalmarkets.xyz/api/waitlist';
const DAY_MS = 24 * 60 * 60 * 1_000;

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function submitWaitlist(overrides: Record<string, string> = {}, origin = 'https://orbitalmarkets.xyz') {
  return exports.default.fetch(WAITLIST_URL, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      origin,
    },
    body: JSON.stringify({
      email: 'Trader@Example.com ',
      profile: 'active_trader',
      monthly_volume: '100k_1m',
      source: 'landing',
      ...overrides,
    }),
  });
}

describe('POST /api/waitlist', () => {
  it('stores a normalized pending request', async () => {
    const response = await submitWaitlist();

    expect(response.status).toBe(202);
    expect(await response.json()).toEqual({ ok: true });
    expect(response.headers.get('access-control-allow-origin')).toBe('https://orbitalmarkets.xyz');

    const row = await env.WAITLIST_DB.prepare(
      'SELECT email, profile, monthly_volume, source, status FROM waitlist_entries',
    ).first();
    expect(row).toEqual({
      email: 'trader@example.com',
      profile: 'active_trader',
      monthly_volume: '100k_1m',
      source: 'landing',
      status: 'pending',
    });
  });

  it('updates a duplicate while it is pending', async () => {
    expect((await submitWaitlist()).status).toBe(202);

    const response = await submitWaitlist({
      email: 'TRADER@example.com',
      profile: 'trading_team',
      monthly_volume: '10m_plus',
    });

    expect(response.status).toBe(202);
    const row = await env.WAITLIST_DB.prepare(
      'SELECT COUNT(*) AS count, profile, monthly_volume, status FROM waitlist_entries',
    ).first();
    expect(row).toEqual({ count: 1, profile: 'trading_team', monthly_volume: '10m_plus', status: 'pending' });
  });

  it('does not mutate a reviewed duplicate', async () => {
    expect((await submitWaitlist()).status).toBe(202);
    await env.WAITLIST_DB.prepare("UPDATE waitlist_entries SET status = 'approved' WHERE email = ?")
      .bind('trader@example.com')
      .run();

    const response = await submitWaitlist({
      email: 'TRADER@example.com',
      profile: 'trading_team',
      monthly_volume: '10m_plus',
    });

    expect(response.status).toBe(202);
    const row = await env.WAITLIST_DB.prepare(
      'SELECT COUNT(*) AS count, profile, monthly_volume, status FROM waitlist_entries',
    ).first();
    expect(row).toEqual({ count: 1, profile: 'active_trader', monthly_volume: '100k_1m', status: 'approved' });
  });

  it.each([
    ['email', 'not-an-email'],
    ['profile', 'invalid'],
    ['monthly_volume', 'invalid'],
    ['source', 'campaign'],
  ])('rejects an invalid %s', async (field, value) => {
    const response = await submitWaitlist({ [field]: value });

    expect(response.status).toBe(400);
    expect(await response.json()).toEqual({ error: 'invalid waitlist request' });
  });

  it('rejects requests from an untrusted origin', async () => {
    const response = await submitWaitlist({ email: 'attacker@example.com' }, 'https://attacker.example');

    expect(response.status).toBe(403);
    expect(
      await env.WAITLIST_DB.prepare('SELECT COUNT(*) AS count FROM waitlist_entries WHERE email = ?')
        .bind('attacker@example.com')
        .first(),
    ).toEqual({ count: 0 });
  });

  it('supports CORS preflight for the landing origin', async () => {
    const response = await exports.default.fetch(WAITLIST_URL, {
      method: 'OPTIONS',
      headers: { origin: 'https://orbitalmarkets.xyz' },
    });

    expect(response.status).toBe(204);
    expect(response.headers.get('access-control-allow-methods')).toBe('POST, OPTIONS');
  });
});

describe('existing gate routes', () => {
  it('keeps non-waitlist API routes hidden without a beta cookie', async () => {
    const response = await exports.default.fetch('https://app.orbitalmarkets.xyz/api/private');

    expect(response.status).toBe(404);
  });

  it('keeps invite redemption validation unchanged', async () => {
    const response = await exports.default.fetch('https://app.orbitalmarkets.xyz/gate/redeem', {
      method: 'POST',
      body: 'not json',
    });

    expect(response.status).toBe(400);
    expect(await response.json()).toEqual({ error: 'invalid json' });
  });
});

describe('beta cookie rolling refresh', () => {
  it('keeps a valid cookie unchanged before the refresh window', async () => {
    const cookie = await redeemInvite('NOREFRESH');
    vi.stubGlobal('fetch', vi.fn(async () => new Response('origin response')));

    const response = await exports.default.fetch('https://app.orbitalmarkets.xyz/dashboard', {
      headers: { cookie },
    });

    expect(response.status).toBe(200);
    expect(await response.text()).toBe('origin response');
    expect(response.headers.get('set-cookie')).toBeNull();
  });

  it('refreshes a valid cookie during the final seven days', async () => {
    const start = new Date('2026-08-07T12:00:00Z');
    vi.useFakeTimers();
    vi.setSystemTime(start);
    const cookie = await redeemInvite('ROLLING');
    const initialExpiration = jwtExpiration(cookie);
    vi.setSystemTime(new Date(start.getTime() + 24 * DAY_MS));
    vi.stubGlobal('fetch', vi.fn(async () => new Response('origin response', { headers: { 'x-origin': 'pages' } })));

    const response = await exports.default.fetch('https://app.orbitalmarkets.xyz/dashboard', {
      headers: { cookie },
    });

    const refreshedCookie = response.headers.get('set-cookie');
    expect(response.status).toBe(200);
    expect(response.headers.get('x-origin')).toBe('pages');
    expect(await response.text()).toBe('origin response');
    expect(refreshedCookie).toContain('__beta=');
    expect(refreshedCookie).toContain('Max-Age=2592000');
    expect(jwtExpiration(refreshedCookie!)).toBe(initialExpiration + 24 * 24 * 60 * 60);
  });

  it('does not refresh an expired cookie on an asset request', async () => {
    const start = new Date('2026-08-07T12:00:00Z');
    vi.useFakeTimers();
    vi.setSystemTime(start);
    const cookie = await redeemInvite('EXPIRED');
    vi.setSystemTime(new Date(start.getTime() + 31 * DAY_MS));
    vi.stubGlobal('fetch', vi.fn(async () => new Response('asset response')));

    const response = await exports.default.fetch('https://app.orbitalmarkets.xyz/app.js', {
      headers: { cookie },
    });

    expect(response.status).toBe(200);
    expect(await response.text()).toBe('asset response');
    expect(response.headers.get('set-cookie')).toBeNull();
  });
});

describe('linked invite redemption', () => {
  it('persists the cookie and user identity link while issuing a uid claim', async () => {
    const timestamp = Math.floor(Date.now() / 1_000);
    const entryId = 'entry-linked';
    const code = 'LINKEDINVITE';
    await env.WAITLIST_DB.prepare(
      `INSERT INTO waitlist_entries
        (id, email, profile, monthly_volume, source, status, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?, 'approved', ?, ?)`,
    )
      .bind(entryId, 'linked@example.com', 'active_trader', '100k_1m', 'landing', timestamp, timestamp)
      .run();
    await env.WAITLIST_DB.prepare(
      `INSERT INTO beta_invites
        (id, waitlist_entry_id, code, status, created_at, delivery_attempts, updated_at)
       VALUES (?, ?, ?, 'sent', ?, 1, ?)`,
    )
      .bind('invite-linked', entryId, code, timestamp, timestamp)
      .run();
    await env.BETA_INVITES.put(`invite:${code}`, JSON.stringify({
      waitlist_entry_id: entryId,
      created_at: timestamp,
    }));

    const response = await exports.default.fetch('https://app.orbitalmarkets.xyz/gate/redeem', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ code }),
    });

    expect(response.status).toBe(200);
    const cookie = response.headers.get('set-cookie');
    expect(cookie).toContain('__beta=');
    const claims = JSON.parse(atob(cookie!.split('=')[1].split('.')[1].replace(/-/g, '+').replace(/_/g, '/'))) as { cid: string; uid: string };
    expect(claims.uid).toBe(entryId);

    const invite = await env.WAITLIST_DB.prepare(
      'SELECT status, bound_cookie_id, redeemed_at FROM beta_invites WHERE code = ?',
    ).bind(code).first<{ status: string; bound_cookie_id: string; redeemed_at: number }>();
    expect(invite?.status).toBe('redeemed');
    expect(invite?.bound_cookie_id).toBe(claims.cid);
    expect(invite?.redeemed_at).toBeTypeOf('number');
  });

  it('continues accepting legacy invite records without a D1 link', async () => {
    const cookie = await redeemInvite('LEGACYLINK');
    expect(cookie).toContain('__beta=');
  });

  it('blocks new redemption after soft revoke while keeping the existing cookie valid', async () => {
    const timestamp = Math.floor(Date.now() / 1_000);
    const entryId = 'entry-soft-revoke';
    const code = 'SOFTREVOKE01';
    await env.WAITLIST_DB.prepare(
      `INSERT INTO waitlist_entries
        (id, email, profile, monthly_volume, source, status, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?, 'approved', ?, ?)`,
    ).bind(entryId, 'soft-revoke@example.com', 'active_trader', '100k_1m', 'landing', timestamp, timestamp).run();
    await env.WAITLIST_DB.prepare(
      `INSERT INTO beta_invites
        (id, waitlist_entry_id, code, status, created_at, delivery_attempts, updated_at)
       VALUES (?, ?, ?, 'sent', ?, 1, ?)`,
    ).bind('invite-soft-revoke', entryId, code, timestamp, timestamp).run();
    await env.BETA_INVITES.put(`invite:${code}`, JSON.stringify({ waitlist_entry_id: entryId, created_at: timestamp }));

    const first = await exports.default.fetch('https://app.orbitalmarkets.xyz/gate/redeem', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ code }),
    });
    const cookie = first.headers.get('set-cookie')!.split(';', 1)[0];
    await env.WAITLIST_DB.prepare("UPDATE beta_invites SET status = 'revoked', revoked_at = ?, updated_at = ? WHERE code = ?")
      .bind(timestamp + 1, timestamp + 1, code).run();
    await env.BETA_INVITES.put(`invite:${code}`, JSON.stringify({ waitlist_entry_id: entryId, created_at: timestamp, revoked_at: timestamp + 1 }));

    const newBrowser = await exports.default.fetch('https://app.orbitalmarkets.xyz/gate/redeem', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ code }),
    });
    expect(newBrowser.status).toBe(404);

    vi.stubGlobal('fetch', vi.fn(async () => new Response('origin response')));
    const existingBrowser = await exports.default.fetch('https://app.orbitalmarkets.xyz/dashboard', { headers: { cookie } });
    expect(existingBrowser.status).toBe(200);
    expect(await existingBrowser.text()).toBe('origin response');
  });
});

async function redeemInvite(code: string): Promise<string> {
  await env.BETA_INVITES.put(`invite:${code}`, JSON.stringify({ created_at: Math.floor(Date.now() / 1_000) }));
  const response = await exports.default.fetch('https://app.orbitalmarkets.xyz/gate/redeem', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ code }),
  });
  expect(response.status).toBe(200);
  return response.headers.get('set-cookie')!.split(';', 1)[0];
}

function jwtExpiration(cookie: string): number {
  const token = cookie.slice(cookie.indexOf('=') + 1);
  const payload = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
  return (JSON.parse(atob(payload)) as { exp: number }).exp;
}
