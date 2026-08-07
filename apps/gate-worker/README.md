# orbital-gate

Cloudflare Worker that owns closed-beta admission and gates `app.<domain>`.

Three responsibilities:

1. **Collect waitlist requests** at `POST /api/waitlist` — validates and stores
   requests in the `WAITLIST_DB` D1 database.
2. **Redeem invite codes** at `POST /gate/redeem` — verifies a code stored in
   the `BETA_INVITES` KV namespace, binds it to a fresh `cookie_id`, mints an
   HS256 JWT, and sets the `__beta` cookie scoped to `.<domain>`.
3. **Edge gate** for everything else — verifies the `__beta` JWT on every
   request. Failures to `/api/*` return 404. Failures to app paths redirect to
   `/gate` (Pages-served static page handles the UI).

Accepted requests fall through to the Pages origin via the Worker route
binding.

## Local dev

```bash
cd apps/gate-worker
pnpm install
cp .dev.vars.example .dev.vars   # fill in JWT_SECRET, COOKIE_DOMAIN
pnpm d1:migrate:local
pnpm dev                          # wrangler dev — local Worker on :8787
```

To seed a test invite locally, hit the dev KV directly via wrangler:

```bash
pnpm --dir ../.. exec wrangler kv:key put --binding=BETA_INVITES --local \
  invite:TEST-CODE-1234 '{"user_label":"local-dev","created_at":1717000000}'
```

Then POST to redeem:

```bash
curl -i -X POST http://localhost:8787/gate/redeem \
  -H 'content-type: application/json' \
  -d '{"code":"TEST-CODE-1234"}'
```

Duplicate requests update qualification data only while the entry is pending.
Approved, rejected, and invited entries are immutable through the public route.

## Verification

```bash
pnpm typecheck
pnpm test
```
