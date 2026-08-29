# Orbital Admin

Separate Cloudflare Worker admin console for closed-beta operations. The Worker
protects admin assets and APIs with a bearer token stored as the `ADMIN_TOKEN`
secret. The admin UI keeps the token only in the current browser session.

## Local development

```bash
pnpm install
pnpm typecheck
pnpm worker:typecheck
pnpm build
```

The Worker fails closed unless requests include a valid bearer token. Set the
token with `wrangler secret put ADMIN_TOKEN` or in the Cloudflare dashboard.

## Deployment

Production is deployed to `admin.orbitalmarkets.xyz` by
`.github/workflows/deploy-admin.yml` after relevant changes land on `master`.
GitHub Actions is the canonical production deployment path.

1. Configure the Cloudflare account, D1, KV, API URL, application origin, and sending toggle as GitHub Actions secrets.
2. Set `ADMIN_TOKEN`, `RESEND_API_KEY`, and `ANALYTICS_API_TOKEN` as Worker secrets.
3. Verify `beta.orbitalmarkets.xyz` in Resend and configure `support@beta.orbitalmarkets.xyz` as the sender.
4. Keep `INVITE_SENDING_ENABLED=false` for the initial deployment and verify reads, approvals, audit records, and KV writes.
5. Set `INVITE_SENDING_ENABLED=true` only for the dedicated production smoke-test window, then run the invite delivery checks.

The D1 migration directory is shared with `apps/gate-worker/migrations` so the
gate and admin Worker remain compatible during rollout.

## Backup and rollback

Before a production migration, export the D1 database with
`wrangler d1 export orbital-waitlist --remote --output waitlist-backup.sql` and
store it outside the repository. Keep the previous Worker version available with
`wrangler versions list`. A bad admin release can be reverted with
`wrangler rollback <VERSION_ID>`. Shared migrations are backward-compatible and
are applied before each Worker deployment.
