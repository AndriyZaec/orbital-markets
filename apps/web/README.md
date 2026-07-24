# Orbital Markets Web

React 19 and TypeScript frontend for the Orbital Markets closed-beta funding
arbitrage terminal.

## Development

Start the Go API on port 8080, then run:

```bash
pnpm install
pnpm dev
```

Vite serves `http://localhost:5173`. Requests under `/api` are proxied to the
local API. The beta Gate Worker is optional locally; when running on port 8787,
Vite also proxies `/gate` to it.

## Checks

```bash
pnpm test
pnpm lint
pnpm build
```

## Structure

- `src/App.tsx` - opportunity discovery and main page state
- `src/components/` - charts, opportunity details, execution UI, and primitives
- `src/hooks/` - API queries and paper/live execution flows
- `src/lib/` - calculations and shared utilities
- `src/providers/` - beta gate and Solana/EVM wallet providers
- `tests/` - Node-based unit tests

See the root `README.md` and `PROJECT_INDEX.md` for product context and the
repository map.
