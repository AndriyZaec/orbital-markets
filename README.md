<p align="center">
  <img src="apps/web/src/assets/orbital-market-banner.png" alt="Orbital Markets" width="1200" />
</p>

# Orbital Market

Orbital Market is an execution copilot for hedged carry trades.

It helps traders and future autonomous agents discover funding opportunities across venues, size them based on real execution quality, open the hedge in a safer way, and manage the position until exit.

## The Problem

Most funding-rate products stop at signal discovery.

They can show a high APY, but they do not answer the hard questions:

- can this trade actually be entered at size?
- how much slippage and fake liquidity risk is hiding underneath?
- how long does the trade need to survive to break even?
- what happens if one leg fills and the other does not?
- when should the trade be closed before the thesis breaks?

Orbital is built to close that gap.

## What Orbital Does

**Three layers. One execution loop. Real carry trades.**

### Market Intelligence
- funding spread discovery
- execution-aware edge ranking
- liquidity and slippage checks

### Execution Engine
- two-leg execution plans
- recommended notional sizing
- hedge integrity controls

### Monitoring & Analytics
- price, funding, and total PnL
- basis and liquidation tracking
- paper execution and post-trade analytics

## Venues

Orbital is currently built around:

- **Pacifica** — Solana
- **Hyperliquid** — Hyperliquid L1

The current live execution foundation is being built venue-by-venue, starting with Pacifica.

The product architecture is intentionally adapter-based, so additional venues can be added without rewriting the execution and monitoring layers.

## Why It Matters

Orbital does not optimize for the prettiest spread table.

It optimizes for the full trade lifecycle:

- better trade selection
- safer hedge entry
- smarter sizing
- degradation and liquidation awareness
- break-even visibility
- real outcome analytics

**Orbital turns funding opportunities into structured, risk-aware execution workflows.**

### Solana Ecosystem Benefits

Orbital is not just another trading UI.

It helps the Solana ecosystem by making fragmented perp liquidity more usable:

- it routes attention and eventually execution toward Solana-native trading venues
- it gives traders a cleaner way to compare funding opportunities instead of leaving liquidity fragmented and opaque
- it improves capital efficiency by helping users size and manage hedged positions more intelligently
- it creates a control layer that future Solana agents, allocators, and automation systems can build on top of

For the hackathon context, Orbital shows how Solana trading infrastructure can evolve from raw venue access into a higher-level execution and risk coordination layer.

## Current Product Surface

### Trading Core
- Pacifica + Hyperliquid market ingestion
- normalized scanner across 292 markets
- execution preview for two-leg trades
- canonical funding and edge model
- BBO-first sizing model
- liquidity labels: `deep`, `medium`, `thin`, `toxic`
- fake-liquidity detection and slippage blockers

### Paper Trading Engine
- paper execution state machine
- partial-fill-aware logic
- retry, unwind, and degraded handling
- auto-close on:
  - degraded state
  - edge collapse
  - critical liquidation risk
- manual close

### Monitoring
- per-leg tracking
- funding PnL, price PnL, total PnL
- basis tracking
- leverage and exposure view
- estimated liquidation price and liquidation distance
- liquidation risk levels: `safe`, `elevated`, `warning`, `critical`

### Analytics
- DB-backed paper analytics
- break-even tracking
- risk-tier and asset breakdowns
- historical edge persistence chart based on real recorded snapshots

### Product Surfaces
- `Fee Rebates` page for GTM narrative
- `Connect Accounts` side panel for multi-venue ops UX
- `For Agents` page positioning Orbital as a control layer for autonomous capital
- demo-friendly `Account Overview` focused on health, exposure, risk, and carry

## Technical Highlights

### Stack
- `apps/api` — Go backend
- `apps/web` — React + Tailwind frontend
- `SQLite` + embedded migrations + `sqlc`

### Core Models
- `Opportunity` — scan and ranking object
- `ExecutionPlan` — concrete open/close plan
- `Position` — tracked trade lifecycle object

### Current Architecture
- off-chain-first execution architecture
- normalized venue adapters
- canonical hourly-normalized funding model
- BBO-first liquidity model with OI as secondary context
- paper execution before live execution

## Execution Semantics

Orbital already models the hard parts of two-leg execution:

- riskier leg first
- leg 2 sized from actual leg 1 fill
- minimum hedgeable fill threshold
- acceptable hedge mismatch band
- retry once -> unwind -> degraded recovery policy
- explicit degraded state in backend and UI

## Live Trading Progress

Pacifica live execution groundwork is already in place:

- private account streams modeled:
  - `account_info`
  - `account_positions`
  - `account_margin`
  - `account_leverage`
  - `account_order_updates`
  - `account_trades`
- pre-trade account validation
- live market-order submit path
- fill/status confirmation model
- live close / unwind path

The next major trading milestone is completing the second venue live path and then wiring the first constrained real two-leg execution loop.

## What Makes Orbital Defensible

Orbital is not just a funding scanner.

Its moat is the control layer between raw venue data and actual execution:

- execution-aware sizing
- fake-liquidity filtering
- hedge integrity rules
- degradation handling
- monitoring and analytics that improve future decisions

This is the layer that traders, desks, and future agents do not want to rebuild venue by venue.

## Business Narrative

Orbital can grow in three directions:

1. trader-facing carry execution workflow
2. premium analytics and monitoring
3. future agent-ready execution and control layer

The product is designed so that humans can use it today, while autonomous capital workflows can build on top of it later.

## Roadmap

### Core execution roadmap
- finish constrained live execution for the first supported venue pair
- harden recovery, unwind, and degraded-state handling
- persist and monitor live positions with the same discipline as paper execution

### Product improvements we can learn from the market
Without changing Orbital's core thesis, there are several things worth borrowing from broader competitors:

- stronger connected-account and signer UX
- more operational portfolio and account surfaces
- cleaner onboarding into venue-linked execution
- better capital mobility and funding/deposit workflows
- a clearer agent-facing control layer once live execution is stable

Our direction remains the same:

**win on execution quality and risk discipline first, then expand product surface.**

## Run Locally

### Backend

```bash
make api-run
```

This starts the Go API from `apps/api`, runs embedded SQLite migrations automatically, and creates/updates `apps/api/orbital.db`.

### Frontend

```bash
cd apps/web
npm install
npm run dev
```

The frontend runs on Vite and talks to the local API.

### Build

Backend:

```bash
make api-build
```

Frontend:

```bash
cd apps/web
npm run build
```

## Status

Today it already has:

- live venue data
- normalized scanner
- execution preview
- paper execution engine
- monitoring and analytics
- early live venue plumbing

The remaining work is not product definition. It is execution hardening.
