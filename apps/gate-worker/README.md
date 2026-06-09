# orbital-gate

Cloudflare Worker that gates `app.<domain>` for the closed beta.

Two responsibilities:

1. **Redeem invite codes** at `POST /gate/redeem` — verifies a code stored in
   the `BETA_INVITES` KV namespace, binds it to a fresh `cookie_id`, mints an
   HS256 JWT, and sets the `__beta` cookie scoped to `.<domain>`.
2. **Edge gate** for everything else — verifies the `__beta` JWT on every
   request. Failures to `/api/*` return 404. Failures to app paths redirect to
   `/gate` (Pages-served static page handles the UI).

Accepted requests fall through to the Pages origin via the Worker route
binding.

## Local dev

```bash
cd apps/gate-worker
pnpm install
cp .dev.vars.example .dev.vars   # fill in JWT_SECRET, COOKIE_DOMAIN
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

## Real KV namespace + secrets

Created once in the CF dashboard or via wrangler:

```bash
pnpm --dir ../.. exec wrangler kv:namespace create BETA_INVITES
# copy the returned id into wrangler.local.toml (gitignored), or override
# wrangler.toml inline before deploy.

pnpm --dir ../.. exec wrangler secret put JWT_SECRET --config wrangler.toml
pnpm --dir ../.. exec wrangler secret put COOKIE_DOMAIN --config wrangler.toml
```

`JWT_SECRET` must match `apps/api`'s `JWT_SECRET` — same value signs and
verifies the cookie across the gate and the API.

## Mint invite codes

```bash
pnpm mint -- --user alice
```

Prints a fresh 12-char code and writes the KV entry. Share the code with the
user out-of-band.

## Deploy

```bash
pnpm deploy
```

Then in the CF dashboard, bind the Worker to:

- `app.<domain>/gate/*`
- `app.<domain>/*`

Rate-limit rule on `/gate/redeem`: 5 req/min per IP (see `.claude/DEPLOY.md`).
