# Project Index

## Deployable applications

### `apps/api`

Go service responsible for venue connectivity, normalized market state,
opportunity discovery, plan construction, paper trading, live execution,
history, and persistence.

```text
cmd/server/                 process entry point and dependency wiring
internal/api/               HTTP routes, auth, and response contracts
internal/db/                SQLite, migrations, snapshots, retention, rollups
internal/domain/            shared market, opportunity, and plan types
internal/executor/          live session state, orchestration, and monitoring
internal/paper/             paper executor, monitor, store, and analytics
internal/scanner/           opportunity generation, sizing, and planning
internal/venue/pacifica/    Pacifica adapter
internal/venue/hyperliquid/ Hyperliquid adapter
```

HTTP routes are registered in `apps/api/internal/api/server.go`:

| Area | Routes |
|---|---|
| System | `GET /api/v1/health` |
| Discovery | `GET /api/v1/markets`, `GET /api/v1/opportunities`, `POST /api/v1/plan` |
| History | `GET /api/v1/history` |
| Paper | `/api/v1/paper/open`, `/api/v1/paper/positions*`, `/api/v1/paper/close/*`, `/api/v1/paper/analytics` |
| Live setup | `/api/v1/live/balances`, `/api/v1/live/accounts/ensure` |
| Live execution | `/api/v1/live/prepare`, `/api/v1/live/advance`, `/api/v1/live/submit` |
| Live positions | `/api/v1/live/positions*`, `/api/v1/live/close/*`, `/api/v1/live/kill` |

Deployment configuration is in `apps/api/fly.toml`; automation is defined in
`.github/workflows/deploy-api.yml`.

### `apps/web`

React 19 application for opportunity discovery, funding and return charts,
paper trading, wallet connections, and non-custodial live execution.

```text
src/App.tsx             primary page and opportunity table
src/components/        charts, panels, dialogs, and shared UI
src/hooks/             API queries and live/paper flows
src/lib/               formatting and calculation helpers
src/providers/         beta gate and Solana/EVM wallet providers
tests/                 Node-based unit tests
```

`apps/web/vite.config.ts` proxies `/api` to the local Go service and `/gate` to
the optional local Worker.

### `apps/gate-worker`

Cloudflare Worker that redeems invite codes from KV, issues the shared beta JWT
cookie, and protects the production application origin.

```text
src/index.ts       request routing and JWT gate
scripts/           invite-code tooling
wrangler.toml      committed Worker configuration template
```

Local and production procedures are in `apps/gate-worker/README.md`.

## Automation

`.github/workflows/` contains deployments for the API and gate Worker. Both
workflows run their relevant checks before deployment. The web application is
deployed separately to Cloudflare Pages.

## Common commands

```bash
# API
cd apps/api && go test ./...
cd apps/api && go run ./cmd/server

# Web
cd apps/web && pnpm install
cd apps/web && pnpm dev
cd apps/web && pnpm test && pnpm lint && pnpm build

# Gate Worker
cd apps/gate-worker && pnpm typecheck
cd apps/gate-worker && pnpm dev
```
