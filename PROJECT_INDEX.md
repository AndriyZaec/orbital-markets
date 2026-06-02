# Project Index: Orbital Market

Updated: 2026-06-01

## Product

Execution-focused perp spread trading product. Orbital detects funding opportunities across Pacifica and Hyperliquid, builds execution plans, simulates and validates them, and tracks hedged positions with explicit risk and degraded-state handling.

V1 is off-chain-first. No custom on-chain program is part of the current release path.

## Project Structure

```text
orbital-market/
├── apps/
│   ├── api/          # Go backend: scanner, paper execution, live execution APIs
│   └── web/          # React + TypeScript frontend
├── packages/
│   └── shared/       # Reserved for shared code/types
├── services/         # Reserved
├── docs/             # Reserved
├── .claude/          # Product steering, execution/risk docs, progress handoff
└── Makefile          # api-build, api-run, api-test
```

## Backend: `apps/api`

Go 1.25, SQLite, embedded migrations, sqlc, HTTP, WebSocket venue integrations.

**Entry point:** `cmd/server/main.go`

### Core Packages

| Package | Purpose |
|---|---|
| `internal/api/` | HTTP server, route wiring, paper handlers, analytics/history handlers, live handlers |
| `internal/domain/` | Opportunity, execution plan, funding, leverage, liquidation, slippage, live admission, signing contracts |
| `internal/scanner/` | Spread scanner, classifier, planner, sizing, liquidity checks |
| `internal/paper/` | Paper executor, monitor, position model, in-memory store, SQLite store |
| `internal/executor/` | Cross-venue live executor, live persistence store, live monitor, execution result model |
| `internal/venue/` | Venue adapter contracts and shared execution interfaces |
| `internal/venue/pacifica/` | Pacifica market data adapter |
| `internal/venue/pacifica/account/` | Pacifica private account streams and pre-trade validation |
| `internal/venue/pacifica/live/` | Pacifica live order client, signed payloads, submit path, fill tracker |
| `internal/venue/hyperliquid/` | Hyperliquid market data adapter and asset map |
| `internal/venue/hyperliquid/account/` | Hyperliquid account state polling and pre-trade validation |
| `internal/venue/hyperliquid/live/` | Hyperliquid live order client, signed payloads, submit path, fill tracker |
| `internal/db/` | SQLite init, migrations, market snapshot recorder |
| `internal/db/sqlc/` | Generated typed queries |

### API Routes

| Method | Route | Purpose |
|---|---|---|
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/markets` | Raw normalized market data |
| GET | `/api/v1/opportunities` | Ranked spread opportunities |
| POST | `/api/v1/plan` | Build execution plan from opportunity |
| POST | `/api/v1/paper/open` | Start paper trade execution |
| GET | `/api/v1/paper/positions` | List paper positions |
| GET | `/api/v1/paper/positions/{id}` | Paper position detail |
| POST | `/api/v1/paper/close/{id}` | Manual close for paper position |
| GET | `/api/v1/paper/analytics` | Paper analytics |
| GET | `/api/v1/history` | Historical market snapshot data |
| POST | `/api/v1/live/prepare` | Build live signing requests for a selected opportunity |
| POST | `/api/v1/live/submit` | Validate and submit a signed venue action |
| GET | `/api/v1/live/positions` | List persisted live positions |
| GET | `/api/v1/live/positions/{id}` | Live position detail with fills/events |
| POST | `/api/v1/live/kill` | Emergency close signing-request preparation for live positions |

### Domain Models

| File | Key Types |
|---|---|
| `opportunity.go` | Opportunity, confidence, risk tier, liquidity tier, direction, venue pair |
| `execution_plan.go` | ExecutionPlan, legs, bounds, side |
| `position.go` | Paper position states and common position concepts |
| `funding.go` | Hourly-normalized funding and canonical edge math |
| `leverage.go` | Leverage config and validation |
| `liquidation.go` | Approximate liquidation price, distance, and risk classification |
| `slippage.go` | Slippage levels, warning/blocker policy |
| `admission.go` | First-live admission gate |
| `signing.go` / `signing_store.go` | Non-custodial signing request and signed action contracts |

### Scanner Pipeline

| File | Purpose |
|---|---|
| `scanner.go` | Main scan loop and pairwise opportunity comparison |
| `classifier.go` | Confidence and risk-tier classification |
| `planner.go` | Fresh execution plan builder with pre-trade checks |
| `sizing.go` | BBO-depth-first sizing model |
| `liquidity_check.go` | Fake-liquidity and market-quality signals |

### Paper Trading Engine

| File | Purpose |
|---|---|
| `position.go` | Paper position struct, execution states, fills, events |
| `executor.go` | State machine: planned -> leg1 -> leg2 -> open/degraded/failed |
| `monitor.go` | Background monitoring: PnL, funding, basis, liquidation, auto-close |
| `store.go` | In-memory store |
| `dbstore.go` | SQLite-backed store and analytics persistence |

### Live Execution Layer

| Area | Status |
|---|---|
| Pacifica live open/close | Implemented via signed payloads and live client submit path |
| Pacifica private account streams | Implemented for account state, margin, leverage, order/trade updates |
| Hyperliquid live open/close | Implemented via EIP-712 signed payloads and live client submit path |
| Hyperliquid account state | Implemented via clearinghouse polling |
| Hyperliquid fill tracking | Implemented via order/fill tracker |
| Shared live executor | Implemented as backend state machine, but must be aligned with signed API flow |
| Live persistence | Implemented tables/store for positions, fills, events |
| Live monitor | Implemented against persisted live positions |
| Live API/UI | Implemented prepare/submit/positions/kill surfaces |

### Current First-Live Gap

The remaining blocker is orchestration correctness, not basic venue plumbing.

The non-custodial live signing flow must be reconciled with the core execution semantics:

- riskier leg first
- leg 2 sized from actual leg 1 fill
- 50% minimum hedgeable fill
- 5% max hedge mismatch
- 5s max wait between legs
- retry once -> unwind -> degraded
- no submission if required signing fails before the execution attempt starts

Until this is resolved, live execution should be treated as built but not first-live validated.

### DB Migrations

| Migration | Purpose |
|---|---|
| `001_paper_positions.sql` | Paper positions, fills, events |
| `002_market_snapshots.sql` | Market snapshots |
| `003_break_even.sql` | Break-even and analytics fields |
| `004_live_positions.sql` | Live positions, fills, execution events |
| `005_live_monitoring.sql` | Live monitoring fields |

## Frontend: `apps/web`

React 19, Vite 8, TypeScript 6, Tailwind 4, shadcn/ui, Solana wallet adapter, wagmi/viem.

**Entry point:** `src/main.tsx` -> `src/App.tsx`

### Components

| Component | Purpose |
|---|---|
| `App.tsx` | Main layout, navigation, trade surface, account panel state |
| `OpportunityPanel` | Opportunity details, leverage, plan summary, live execution trigger |
| `LiveExecutionModal` | Prepare/sign/submit flow status and confirmation UI |
| `ConnectAccounts` | Multi-venue wallet/account readiness panel |
| `PaperPositions` | Paper position list and manual close actions |
| `PositionDetail` | Paper position fill, PnL, basis, liquidation, event detail |
| `LivePositions` | Live position list |
| `LivePositionDetail` | Live position fills/events and monitoring detail |
| `AnalyticsDashboard` | Paper analytics and account overview style metrics |
| `FundingChart` | Historical basis and annualized edge chart from backend snapshots |
| `FeeRebates` | GTM/demo narrative surface |
| `ForAgents` | Agent-control-layer narrative surface |
| `ui/*` | shadcn/ui primitives |

### Hooks And Client Helpers

| File | Purpose |
|---|---|
| `useOpportunities` | Polls opportunities |
| `usePlan` | Fetches execution plan with leverage |
| `usePaperPositions` | Polls paper positions and closes paper positions |
| `useLivePositions` | Polls live positions |
| `useLiveExecution` | Frontend live prepare/sign/submit orchestration |
| `useKillSwitch` | Emergency close signing/submission flow |
| `useVenueAuthority` | Connected Solana/EVM account authority state |
| `useAnalytics` | Polls paper analytics |
| `useHistory` | Fetches historical snapshot series |
| `lib/signing/*` | Pacifica and Hyperliquid signing helpers |
| `types/signing.ts` | Frontend signing contracts |

## Venue Support

| Venue | Market Data | BBO | Account State | Live Orders | Status |
|---|---|---|---|---|---|
| Pacifica | WS prices | WS BBO | Private WS account streams | Signed open + close/unwind | Primary live venue |
| Hyperliquid | REST metadata + WS BBO | WS BBO | Clearinghouse polling + trackers | Signed open + close/unwind | Primary live venue |

## Build And Run

```bash
make api-run
cd apps/web && npm run dev
make api-build
make api-test
```

Frontend build:

```bash
cd apps/web && npm run build
```

## Documentation Map

| File | Purpose |
|---|---|
| `.claude/CLAUDE.md` | Product steering and scope constraints |
| `.claude/ARCHITECTURE.md` | Off-chain-first architecture |
| `.claude/EXECUTION.md` | Execution state machine and signing rules |
| `.claude/RISK_MODEL.md` | Risk model and guardrails |
| `.claude/PROGRESS.md` | Progress log and immediate priorities |
| `.claude/SESSION_HANDOFF.md` | Current handoff for new sessions |
| `.claude/SPEC_GAP_ANALYSIS.md` | Gap analysis against engine spec |
| `.claude/HACKS.md` | Resolved mock data and remaining placeholder fields |
| `.claude/UI_CHANGELOG.md` | UI evolution notes |
