// Cloudflare Worker for the closed-beta gate. Bound to app.<domain>/*.
//
// Responsibilities:
//   1. POST /gate/redeem — verify invite code in KV, bind to a fresh cookie_id,
//      sign HS256 JWT, set `__beta` cookie scoped to .<domain>.
//   2. Everything else — verify `__beta` JWT. Failures redirect to /gate
//      (app paths) or return 404 (defensive /api/*). Successes fall through
//      to the Pages origin.
//
// The Go API on api.<domain> verifies the same JWT (same JWT_SECRET).

export interface Env {
  BETA_INVITES: KVNamespace;
  JWT_SECRET: string;
  COOKIE_DOMAIN: string; // e.g. ".your-domain.example"; empty = no Domain attribute (local dev)
}

const COOKIE_NAME = '__beta';
const COOKIE_MAX_AGE = 30 * 24 * 60 * 60; // seconds
const GATE_PATH = '/gate';

interface InviteRecord {
  user_label?: string;
  created_at: number;
  used_at?: number;
  bound_cookie_id?: string;
  revoked_at?: number;
}

interface Claims {
  cid: string;
  exp: number;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === '/gate/redeem' && request.method === 'POST') {
      return handleRedeem(request, env);
    }

    // The gate page itself is served unauthenticated so users can land on it.
    if (url.pathname === GATE_PATH || url.pathname.startsWith(GATE_PATH + '/')) {
      return fetch(request);
    }

    const token = readCookie(request, COOKIE_NAME);
    const ok = token !== null && (await verifyJWT(token, env.JWT_SECRET));
    if (!ok) {
      // Defensive: in case the Worker route ever catches a stray /api/* path.
      if (url.pathname.startsWith('/api/')) {
        return new Response('Not Found', { status: 404 });
      }
      return Response.redirect(new URL(GATE_PATH, url.origin).toString(), 302);
    }

    return fetch(request);
  },
} satisfies ExportedHandler<Env>;

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
    const jwt = await signJWT({ cid: existing.cid, exp: now() + COOKIE_MAX_AGE }, env.JWT_SECRET);
    return setCookieResponse(jwt, env.COOKIE_DOMAIN);
  }

  // First-time redeem: mint cookie_id, bind in KV, set cookie.
  const cid = randomHex(16);
  const jwt = await signJWT({ cid, exp: now() + COOKIE_MAX_AGE }, env.JWT_SECRET);
  const updated: InviteRecord = {
    ...record,
    used_at: now(),
    bound_cookie_id: cid,
  };
  await env.BETA_INVITES.put(key, JSON.stringify(updated));
  return setCookieResponse(jwt, env.COOKIE_DOMAIN);
}

function setCookieResponse(jwt: string, cookieDomain: string): Response {
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
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: {
      'content-type': 'application/json',
      'set-cookie': parts.join('; '),
    },
  });
}

function jsonResponse(status: number, body: object): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
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

async function verifyJWT(token: string, secret: string): Promise<boolean> {
  const claims = await decodeJWT(token, secret);
  return claims !== null && claims.exp > now();
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
    return JSON.parse(TD.decode(b64urlDecode(p))) as Claims;
  } catch {
    return null;
  }
}
