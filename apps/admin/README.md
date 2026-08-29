# Orbital Admin

Separate Cloudflare Worker admin console for closed-beta operations. Cloudflare
Access protects the hostname, and the Worker independently validates the
`Cf-Access-Jwt-Assertion` header before serving assets or admin APIs.

## Local development

```bash
pnpm install
pnpm typecheck
pnpm worker:typecheck
pnpm build
```

The local Worker fails closed unless requests include a valid Access assertion.
Do not add a production bypass. Configure a local Access test token only in a
gitignored environment when exercising the Worker directly.

## Deployment

1. Configure a Cloudflare Access self-hosted application for the admin hostname and restrict it to the operator group.
2. Set `TEAM_DOMAIN` and `POLICY_AUD` to that application's values.
3. Replace the placeholder D1/KV identifiers in a local deployment config.
4. Onboard the sender domain and configure the Email Sending binding.
5. Set `ANALYTICS_API_TOKEN` as a Worker secret and point `ANALYTICS_API_URL` at the Go API.
6. Keep `INVITE_SENDING_ENABLED=false` for the initial deployment and verify reads, approvals, audit records, and KV writes.
7. Set `INVITE_SENDING_ENABLED=true` only for the dedicated production smoke-test window, then run the invite delivery checks.
8. Run `pnpm deploy`.

The D1 migration directory is shared with `apps/gate-worker/migrations` so the
gate and admin Worker remain compatible during rollout.

## Backup and rollback

Before a production migration, export the D1 database with
`wrangler d1 export orbital-waitlist --remote --output waitlist-backup.sql` and
store it outside the repository. Keep the previous Worker version available with
`wrangler versions list`. A bad admin release can be reverted with
`wrangler rollback <VERSION_ID>`; gate migrations are backward-compatible and
must be rolled out before either Worker is switched to the new behavior.
