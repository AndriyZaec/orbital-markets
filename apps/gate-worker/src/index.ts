// Cloudflare Worker for the closed-beta gate. Bound to app.<domain>/*.
//
// Responsibilities:
//   1. POST /api/waitlist — accept public beta access requests into D1.
//   2. POST /gate/redeem — verify invite code in KV, bind to a fresh cookie_id,
//      sign HS256 JWT, set `__beta` cookie scoped to .<domain>.
//   3. Everything else — verify `__beta` JWT. Failures redirect to /gate
//      (app paths) or return 404 (defensive /api/*). Successes fall through
//      to the Pages origin.
//
// The Go API on api.<domain> verifies the same JWT (same JWT_SECRET).

export interface Env {
  BETA_INVITES: KVNamespace;
  WAITLIST_DB: D1Database;
  JWT_SECRET: string;
  COOKIE_DOMAIN: string; // e.g. ".your-domain.example"; empty = no Domain attribute (local dev)
  LANDING_ORIGIN?: string;
}

const COOKIE_NAME = '__beta';
const COOKIE_MAX_AGE = 30 * 24 * 60 * 60; // seconds
const COOKIE_REFRESH_WINDOW = 7 * 24 * 60 * 60; // seconds
const GATE_PATH = '/gate';
const WAITLIST_PATH = '/api/waitlist';
const MAX_WAITLIST_BODY_BYTES = 4_096;
const WAITLIST_PROFILES = new Set([
  'active_trader',
  'occasional_trader',
  'defi_user',
  'return_seeker',
  'trading_team',
  'researching',
]);
const WAITLIST_VOLUMES = new Set([
  'under_1k',
  '1k_10k',
  'under_10k',
  '10k_50k',
  '50k_100k',
  '100k_1m',
  '1m_plus',
  '1m_10m',
  '10m_plus',
]);

interface InviteRecord {
  user_label?: string;
  waitlist_entry_id?: string;
  created_at: number;
  used_at?: number;
  bound_cookie_id?: string;
  revoked_at?: number;
}

interface Claims {
  cid: string;
  uid?: string;
  exp: number;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === WAITLIST_PATH) {
      return handleWaitlistRoute(request, env, url);
    }

    if (url.pathname === '/gate/redeem' && request.method === 'POST') {
      return handleRedeem(request, env);
    }

    // The gate page itself is served unauthenticated so users can land on it.
    if (url.pathname === GATE_PATH || url.pathname.startsWith(GATE_PATH + '/')) {
      return fetch(request);
    }

    const token = readCookie(request, COOKIE_NAME);
    const claims = await decodeJWT(token, env.JWT_SECRET);
    const currentTime = now();
    if (!claims || claims.exp <= currentTime) {
      // Defensive: in case the Worker route ever catches a stray /api/* path.
      if (url.pathname.startsWith('/api/')) {
        return new Response('Not Found', { status: 404 });
      }
      // Only redirect document navigations. Asset fetches (JS/CSS/img/etc.)
      // must pass through so the gate page can load its own bundle.
      const dest = request.headers.get('sec-fetch-dest');
      const accept = request.headers.get('accept') ?? '';
      const isDocument = dest === 'document' || (dest === null && accept.includes('text/html'));
      if (isDocument) {
        return Response.redirect(new URL(GATE_PATH, url.origin).toString(), 302);
      }
      return fetch(request);
    }

    const response = await fetch(request);
    if (claims.exp - currentTime > COOKIE_REFRESH_WINDOW) {
      return response;
    }

    const jwt = await signJWT({ cid: claims.cid, uid: claims.uid, exp: currentTime + COOKIE_MAX_AGE }, env.JWT_SECRET);
    return withCookie(response, jwt, env.COOKIE_DOMAIN);
  },
} satisfies ExportedHandler<Env>;

async function handleWaitlistRoute(request: Request, env: Env, url: URL): Promise<Response> {
  const origin = request.headers.get('origin');
  const corsHeaders = waitlistCorsHeaders(origin, env, url.origin);
  if (origin && !corsHeaders) {
    return jsonResponse(403, { error: 'origin not allowed' });
  }
  const responseHeaders = corsHeaders ?? {};

  if (request.method === 'OPTIONS') {
    return new Response(null, {
      status: 204,
      headers: {
        ...responseHeaders,
        'access-control-allow-headers': 'content-type',
        'access-control-allow-methods': 'POST, OPTIONS',
        'access-control-max-age': '86400',
      },
    });
  }
  if (request.method !== 'POST') {
    return jsonResponse(405, { error: 'method not allowed' }, { ...responseHeaders, allow: 'POST, OPTIONS' });
  }

  try {
    const body = await readJsonBody(request);
    const entry = validateWaitlistEntry(body);
    if (!entry) {
      return jsonResponse(400, { error: 'invalid waitlist request' }, responseHeaders);
    }

    const timestamp = now();
    const result = await env.WAITLIST_DB.prepare(
      `INSERT INTO waitlist_entries
        (id, email, profile, monthly_volume, source, status, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)
       ON CONFLICT(email) DO UPDATE SET
         profile = excluded.profile,
         monthly_volume = excluded.monthly_volume,
         source = excluded.source,
         updated_at = excluded.updated_at
       WHERE waitlist_entries.status = 'pending'`,
    )
      .bind(crypto.randomUUID(), entry.email, entry.profile, entry.monthlyVolume, entry.source, timestamp, timestamp)
      .run();
    if (!result.success) {
      throw new Error('D1 write was not successful');
    }
    return jsonResponse(202, { ok: true }, responseHeaders);
  } catch (error) {
    if (error instanceof WaitlistRequestError) {
      return jsonResponse(error.status, { error: error.message }, responseHeaders);
    }
    console.error(
      JSON.stringify({
        message: 'waitlist request failed',
        error: error instanceof Error ? error.message : String(error),
        path: url.pathname,
      }),
    );
    return jsonResponse(500, { error: 'internal server error' }, responseHeaders);
  }
}

interface WaitlistEntry {
  email: string;
  profile: string;
  monthlyVolume: string;
  source: string;
}

function validateWaitlistEntry(value: unknown): WaitlistEntry | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const body = value as Record<string, unknown>;
  if (
    typeof body.email !== 'string' ||
    typeof body.profile !== 'string' ||
    typeof body.monthly_volume !== 'string' ||
    body.source !== 'landing'
  ) {
    return null;
  }

  const email = body.email.trim().toLowerCase();
  if (email.length > 254 || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) return null;
  if (!WAITLIST_PROFILES.has(body.profile) || !WAITLIST_VOLUMES.has(body.monthly_volume)) return null;

  return {
    email,
    profile: body.profile,
    monthlyVolume: body.monthly_volume,
    source: body.source,
  };
}

class WaitlistRequestError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

async function readJsonBody(request: Request): Promise<unknown> {
  const contentLength = request.headers.get('content-length');
  if (contentLength && Number(contentLength) > MAX_WAITLIST_BODY_BYTES) {
    throw new WaitlistRequestError(413, 'payload too large');
  }
  if (!request.body) {
    throw new WaitlistRequestError(400, 'invalid json');
  }

  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > MAX_WAITLIST_BODY_BYTES) {
      await reader.cancel();
      throw new WaitlistRequestError(413, 'payload too large');
    }
    chunks.push(value);
  }

  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  try {
    return JSON.parse(new TextDecoder().decode(bytes)) as unknown;
  } catch {
    throw new WaitlistRequestError(400, 'invalid json');
  }
}

function waitlistCorsHeaders(origin: string | null, env: Env, requestOrigin: string): Record<string, string> | null {
  if (!origin) return {};
  if (origin !== requestOrigin && origin !== env.LANDING_ORIGIN) return null;
  return {
    'access-control-allow-origin': origin,
    vary: 'Origin',
  };
}

async function handleRedeem(request: Request, env: Env): Promise<Response> {
  let body: { code?: string };
  try {
    body = (await request.json()) as { code?: string };
  } catch {
    return jsonResponse(400, { error: 'invalid json' });
  }
  // Normalize: strip whitespace and dashes, uppercase. Codes are Crockford
  // base32 — see scripts/mint-invite.ts.
  const code = (body.code ?? '').replace(/[\s-]/g, '').toUpperCase();
  if (!code) {
    return jsonResponse(400, { error: 'code required' });
  }

  const key = `invite:${code}`;
  const raw = await env.BETA_INVITES.get(key);
  if (!raw) {
    // 404, not a verbose "unknown code" — matches the gate's stealth posture.
    return jsonResponse(404, { error: 'invalid code' });
  }

  let record: InviteRecord;
  try {
    record = JSON.parse(raw) as InviteRecord;
  } catch {
    return jsonResponse(500, { error: 'corrupt invite record' });
  }
  if (record.revoked_at) {
    return jsonResponse(404, { error: 'invalid code' });
  }

  // Already-redeemed code: only the originally-bound browser may refresh it.
  // Anyone else (cleared cookie, different browser) is locked out — by design.
  if (record.used_at && record.bound_cookie_id) {
    const existing = await decodeJWT(readCookie(request, COOKIE_NAME), env.JWT_SECRET);
    if (!existing || existing.cid !== record.bound_cookie_id) {
      return jsonResponse(404, { error: 'invalid code' });
    }
    const jwt = await signJWT({ cid: existing.cid, uid: record.waitlist_entry_id, exp: now() + COOKIE_MAX_AGE }, env.JWT_SECRET);
    return setCookieResponse(jwt, env.COOKIE_DOMAIN);
  }

  // New admin-issued invites are linked to D1 before the KV record is updated.
  // If the KV write fails, a retry can recover the same cookie id from D1.
  const linkedCookieId = record.waitlist_entry_id
    ? await redeemedCookieId(env, record.waitlist_entry_id, code)
    : null;
  const cid = linkedCookieId ?? randomHex(16);
  const redeemedAt = now();
  if (record.waitlist_entry_id && !linkedCookieId) {
    await persistRedemption(env, record.waitlist_entry_id, code, cid, redeemedAt);
  }
  const jwt = await signJWT({ cid, uid: record.waitlist_entry_id, exp: now() + COOKIE_MAX_AGE }, env.JWT_SECRET);
  const updated: InviteRecord = {
    ...record,
    used_at: redeemedAt,
    bound_cookie_id: cid,
  };
  await env.BETA_INVITES.put(key, JSON.stringify(updated));
  return setCookieResponse(jwt, env.COOKIE_DOMAIN);
}

async function redeemedCookieId(env: Env, waitlistEntryId: string, code: string): Promise<string | null> {
  const row = await env.WAITLIST_DB.prepare(
    'SELECT bound_cookie_id FROM beta_invites WHERE waitlist_entry_id = ? AND code = ? AND status = \'redeemed\'',
  )
    .bind(waitlistEntryId, code)
    .first<{ bound_cookie_id: string | null }>();
  return row?.bound_cookie_id ?? null;
}

async function persistRedemption(
  env: Env,
  waitlistEntryId: string,
  code: string,
  cookieId: string,
  redeemedAt: number,
): Promise<void> {
  const result = await env.WAITLIST_DB.prepare(
    `UPDATE beta_invites
        SET status = 'redeemed', bound_cookie_id = ?, redeemed_at = ?, updated_at = ?
      WHERE waitlist_entry_id = ?
        AND code = ?
        AND status IN ('issued', 'sent', 'delivery_failed')`,
  )
    .bind(cookieId, redeemedAt, redeemedAt, waitlistEntryId, code)
    .run();
  if (!result.success || result.meta.changes !== 1) {
    throw new Error('invite redemption linkage failed');
  }
}

function setCookieResponse(jwt: string, cookieDomain: string): Response {
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: {
      'content-type': 'application/json',
      'set-cookie': cookieHeader(jwt, cookieDomain),
    },
  });
}

function withCookie(response: Response, jwt: string, cookieDomain: string): Response {
  const headers = new Headers(response.headers);
  headers.append('set-cookie', cookieHeader(jwt, cookieDomain));
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

function cookieHeader(jwt: string, cookieDomain: string): string {
  const parts = [
    `${COOKIE_NAME}=${jwt}`,
    'Path=/',
    'HttpOnly',
    'SameSite=Strict',
    `Max-Age=${COOKIE_MAX_AGE}`,
  ];
  if (cookieDomain) {
    parts.push(`Domain=${cookieDomain}`);
    parts.push('Secure'); // production scope assumed when Domain is set
  }
  return parts.join('; ');
}

function jsonResponse(status: number, body: object, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { ...headers, 'content-type': 'application/json' },
  });
}

function now(): number {
  return Math.floor(Date.now() / 1000);
}

function randomHex(byteLen: number): string {
  const buf = new Uint8Array(byteLen);
  crypto.getRandomValues(buf);
  return Array.from(buf, (b) => b.toString(16).padStart(2, '0')).join('');
}

function readCookie(request: Request, name: string): string | null {
  const header = request.headers.get('cookie');
  if (!header) return null;
  const m = header.match(new RegExp(`(?:^|; *)${name}=([^;]+)`));
  return m ? decodeURIComponent(m[1]) : null;
}

// --- JWT helpers (HS256, single algorithm) -----------------------------------

const TE = new TextEncoder();
const TD = new TextDecoder();

function b64urlEncode(data: Uint8Array | string): string {
  const bytes = typeof data === 'string' ? TE.encode(data) : data;
  let s = '';
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function b64urlDecode(s: string): Uint8Array {
  const padded = s.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((s.length + 3) % 4);
  const bin = atob(padded);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function hmacKey(secret: string, usage: 'sign' | 'verify'): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', TE.encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, [usage]);
}

async function signJWT(claims: Claims, secret: string): Promise<string> {
  const header = b64urlEncode(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
  const payload = b64urlEncode(JSON.stringify(claims));
  const signing = `${header}.${payload}`;
  const key = await hmacKey(secret, 'sign');
  const sig = new Uint8Array(await crypto.subtle.sign('HMAC', key, TE.encode(signing)));
  return `${signing}.${b64urlEncode(sig)}`;
}

async function decodeJWT(token: string | null, secret: string): Promise<Claims | null> {
  if (!token) return null;
  const parts = token.split('.');
  if (parts.length !== 3) return null;
  const [h, p, s] = parts;
  let header: { alg?: string };
  try {
    header = JSON.parse(TD.decode(b64urlDecode(h)));
  } catch {
    return null;
  }
  if (header.alg !== 'HS256') return null;
  const key = await hmacKey(secret, 'verify');
  const ok = await crypto.subtle.verify('HMAC', key, b64urlDecode(s), TE.encode(`${h}.${p}`));
  if (!ok) return null;
  try {
    const claims = JSON.parse(TD.decode(b64urlDecode(p))) as Partial<Claims>;
    if (typeof claims.cid !== 'string' || typeof claims.exp !== 'number') return null;
    return claims as Claims;
  } catch {
    return null;
  }
}
