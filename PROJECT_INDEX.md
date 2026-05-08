# Project Index: Orbital Market

Generated: 2026-05-08

## Product

Execution-focused perp spread trading product on Solana. Detects spread opportunities across venues, builds execution plans, simulates before submission, and tracks positions. Off-chain first (v1).

## Project Structure

```
orbital-market/
├── apps/
│   ├── api/          # Go backend (HTTP + WebSocket)
│   └── web/          # React + TypeScript frontend (Vite)
├── packages/
│   └── shared/       # Shared types (currently empty)
├── services/         # (empty, reserved)
├── docs/             # (empty, reserved)
└── Makefile          # api-build, api-run, api-test
```

## Backend (apps/api) — Go 1.25, SQLite, WebSocket

**Entry point:** `cmd/server/main.go`

### Core Packages

| Package | Purpose |
|---|---|
| `internal/api/` | HTTP server, paper trading handlers, analytics handlers |
| `internal/domain/` | Domain models: opportunity, position, execution_plan, funding, leverage, liquidation, slippage |
| `internal/scanner/` | Spread scanner, classifier, planner, sizing, liquidity_check |
| `internal/paper/` | Paper trading engine: executor, monitor, position, store (in-memory + DB) |
| `internal/venue/` | Venue adapter interface |
| `internal/venue/pacifica/` | Pacifica venue: client, orders, fills, account state, subscriber, tracker |
| `internal/venue/hyperliquid/` | Hyperliquid venue adapter |
| `internal/db/` | SQLite DB, migrations (goose), sqlc-generated queries |

### Key Dependencies

- `gorilla/websocket` — WebSocket connections
- `pressly/goose` — DB migrations
- `modernc.org/sqlite` — Pure-Go SQLite driver
- `sqlc` — SQL-to-Go code generation

### DB Migrations

1. `001_paper_positions.sql` — paper position tracking
2. `002_market_snapshots.sql` — market data snapshots
3. `003_break_even.sql` — break-even analytics

## Frontend (apps/web) — React 19, Vite 8, TypeScript 6, Tailwind 4

**Entry point:** `src/main.tsx` → `src/App.tsx`

### Components

| Component | Purpose |
|---|---|
| `OpportunityPanel` | Displays detected spread opportunities |
| `PlanPreview` | Shows execution plan before submission |
| `PaperPositions` | Lists paper trading positions |
| `PositionDetail` | Detailed view of a single position |
| `AnalyticsDashboard` | Performance analytics and metrics |
| `ui/*` | shadcn/ui primitives (button, card, badge, dialog, input, table, tabs, separator, progress) |

### Hooks

| Hook | Purpose |
|---|---|
| `useOpportunities` | Fetches spread opportunities from API |
| `usePlan` | Manages execution plan lifecycle |
| `usePaperPositions` | Paper position CRUD |
| `useAnalytics` | Analytics data fetching |

### Key Dependencies

- `react` 19, `react-dom` 19
- `tailwindcss` 4, `@tailwindcss/vite`
- `shadcn` 4 + `@base-ui/react`
- `lucide-react` — icons
- `class-variance-authority`, `clsx`, `tailwind-merge` — styling utils

## Build & Run

```bash
make api-run          # Start Go backend
cd apps/web && npm run dev   # Start Vite dev server
make api-build        # Build Go binary
make api-test         # Run Go tests
```

## Venue Support (Current)

- **Pacifica** — primary venue, on-chain Solana perps (full integration: orders, fills, account state, tracking)
- **Hyperliquid** — secondary venue adapter

## Architecture Notes

- Off-chain spread detection → execution plan → simulate → submit → track
- Paper trading mode available for risk-free testing
- SQLite for local persistence (positions, snapshots, analytics)
- WebSocket for real-time market data and position updates
- No on-chain program in v1
