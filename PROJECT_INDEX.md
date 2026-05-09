# Project Index: Orbital Market

Generated: 2026-05-09

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
| `internal/api/` | HTTP server, route wiring, paper/analytics/history handlers |
| `internal/domain/` | Domain models: opportunity, position, execution_plan, funding, leverage, liquidation, slippage |
| `internal/scanner/` | Spread scanner, classifier, planner, sizing, liquidity_check |
| `internal/paper/` | Paper trading engine: executor, monitor, position, store (in-memory), dbstore (SQLite) |
| `internal/venue/` | Venue adapter interface + MarketData contract |
| `internal/venue/pacifica/` | Pacifica market data adapter (WS: prices + BBO per symbol) |
| `internal/venue/pacifica/account/` | Pacifica private account state: subscriber, state model, pre-trade validation |
| `internal/venue/pacifica/live/` | Pacifica live execution: client (open + close), order model, fill model, tracker |
| `internal/venue/hyperliquid/` | Hyperliquid venue adapter (REST + WS BBO) |
| `internal/db/` | SQLite init, migrations (goose embedded), snapshot recorder |
| `internal/db/sqlc/` | sqlc-generated typed queries |

### API Routes

| Method | Route | Purpose |
|---|---|---|
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/markets` | Raw market data from all venues |
| GET | `/api/v1/opportunities` | Ranked spread opportunities |
| POST | `/api/v1/plan` | Build execution plan from opportunity |
| POST | `/api/v1/paper/open` | Start paper trade execution |
| GET | `/api/v1/paper/positions` | List paper positions |
| GET | `/api/v1/paper/positions/{id}` | Single position detail |
| POST | `/api/v1/paper/close/{id}` | Manual close paper position |
| GET | `/api/v1/paper/analytics` | Paper trading analytics |
| GET | `/api/v1/history` | Historical market snapshot data |

### Domain Models

| File | Key Types |
|---|---|
| `opportunity.go` | Opportunity, Confidence, RiskTier, LiquidityTier, Direction, VenuePair |
| `execution_plan.go` | ExecutionPlan, Leg, Bounds, Side |
| `position.go` | Position, PositionState |
| `funding.go` | HoursPerYear, AnnualizeRate, DeannualizeRate, CarryEdgePerHour, EstimatedNetEdge |
| `leverage.go` | LeverageConfig, ComputeLeverage, ValidateLeverage |
| `liquidation.go` | LiquidationPrice, LiquidationDistance, ClassifyLiqRisk, LiqRiskLevel |
| `slippage.go` | ClassifySlippage, SlippageLevel, SlippageExecutable |

### Scanner Pipeline

| File | Purpose |
|---|---|
| `scanner.go` | Main scan loop, snapshot collection, pairwise opportunity comparison |
| `classifier.go` | Confidence, risk tier classification |
| `planner.go` | Fresh execution plan builder with pre-trade checks |
| `sizing.go` | BBO-depth-first sizing model, sqrt impact, geometric search |
| `liquidity_check.go` | Fake-liquidity detection signals |

### Paper Trading Engine

| File | Purpose |
|---|---|
| `position.go` | Paper position struct, execution states, fill model, events |
| `executor.go` | State machine: planned → leg1 → leg2 → open/degraded/failed |
| `monitor.go` | Background loop: P&L, basis, liquidation, auto-close |
| `store.go` | In-memory position store |
| `dbstore.go` | SQLite-backed store, break-even computation |

### Pacifica Live Execution (not yet wired to executor)

| File | Purpose |
|---|---|
| `account/state.go` | Account equity, margin, positions, per-symbol config |
| `account/subscriber.go` | Private WS: account_info, positions, margin, leverage, orders, trades |
| `account/pretrade.go` | Pre-trade validation: margin, leverage, freshness checks |
| `live/order.go` | MarketOrderRequest, SubmitResult |
| `live/client.go` | SubmitMarketOrder (open), SubmitCloseOrder (reduce-only unwind) |
| `live/fill.go` | OrderStatus lifecycle, FillResult |
| `live/tracker.go` | Order/fill correlation by clientOrderID, WaitForFill |

### Key Dependencies

- `gorilla/websocket` — WebSocket connections
- `pressly/goose` — DB migrations
- `modernc.org/sqlite` — Pure-Go SQLite driver
- `sqlc` — SQL-to-Go code generation

### DB Migrations

1. `001_paper_positions.sql` — paper positions, fills, events
2. `002_market_snapshots.sql` — market data snapshots
3. `003_break_even.sql` — break-even + risk tier + hold hours

## Frontend (apps/web) — React 19, Vite 8, TypeScript 6, Tailwind 4

**Entry point:** `src/main.tsx` → `src/App.tsx`

### Components

| Component | Purpose |
|---|---|
| `App.tsx` | Main layout: header, opportunity table, positions panel, nav |
| `OpportunityPanel` | Side panel: opportunity detail, leverage slider, leg preview, execute button |
| `PlanPreview` | Modal: execution plan with leverage selector, bounds, warnings |
| `PaperPositions` | Positions table with open/closed tabs, close button, venue icons |
| `PositionDetail` | Modal: fill cards, metrics, basis, liquidation, event timeline |
| `AnalyticsDashboard` | KPI cards, P&L breakdown, break-even, by-asset/risk/close-reason tables |
| `FundingChart` | Historical funding edge chart from snapshot data |
| `ui/*` | shadcn/ui primitives |

### Hooks

| Hook | Purpose |
|---|---|
| `useOpportunities` | Polls `/api/v1/opportunities` every 10s |
| `usePlan` | Fetches execution plan with leverage, auto-refreshes |
| `usePaperPositions` | Polls positions, provides closePosition action |
| `useAnalytics` | Polls `/api/v1/paper/analytics` every 15s |
| `useHistory` | Fetches historical snapshot data for charts |

### Other

| File | Purpose |
|---|---|
| `lib/hacks.ts` | Mock data helpers (leverage caps, APR estimates, volume) |
| `lib/utils.ts` | `cn()` utility for class merging |

### Key Dependencies

- `react` 19, `react-dom` 19
- `tailwindcss` 4, `@tailwindcss/vite`
- `shadcn` 4 + `@base-ui/react`
- `lucide-react` — icons
- `class-variance-authority`, `clsx`, `tailwind-merge` — styling utils

## Build & Run

```bash
make api-run                    # Start Go backend (port 8080)
cd apps/web && npm run dev      # Start Vite dev server (port 5173, proxies /api)
make api-build                  # Build Go binary
make api-test                   # Run Go tests
```

## Venue Support

| Venue | Market Data | BBO | Account State | Live Orders | Status |
|---|---|---|---|---|---|
| Pacifica | WS prices | WS bbo per symbol | Private WS (skeleton) | Open + Close (skeleton) | Primary |
| Hyperliquid | REST meta + WS bbo | WS bbo | — | — | Secondary |

## Architecture Notes

- Off-chain spread detection → execution plan → simulate → submit → track
- Paper trading mode validates execution semantics before live
- SQLite for local persistence (positions, snapshots, analytics)
- WebSocket for real-time market data from both venues
- Pacifica BBO provides real bid/ask/size (not mid-only)
- Auto-close triggers: degraded state, edge collapse, critical liquidation risk
- No on-chain program in v1
