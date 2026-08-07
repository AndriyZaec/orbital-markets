import { env, exports } from 'cloudflare:workers';
import { describe, expect, it } from 'vitest';

const WAITLIST_URL = 'https://orbitalmarkets.xyz/api/waitlist';

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
