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

Orbital currently supports:

- **Pacifica** - Solana
- **Hyperliquid** - Hyperliquid L1

Both venues provide live market data, account readiness, signed order submission, fill tracking, and reduce-only close paths.

The product architecture is intentionally adapter-based, so additional venues can be added without rewriting the execution and monitoring layers. Venue expansion remains secondary to validating retention and executable economics on the current pair.

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

- it routes attention and execution toward Solana-native trading venues
- it gives traders a cleaner way to compare funding opportunities instead of leaving liquidity fragmented and opaque
- it improves capital efficiency by helping users size and manage hedged positions more intelligently
- it creates a control layer that future Solana agents, allocators, and automation systems can build on top of

For the hackathon context, Orbital shows how Solana trading infrastructure can evolve from raw venue access into a higher-level execution and risk coordination layer.

## Current Product Surface

### Trading Core
- Pacifica + Hyperliquid market ingestion
- normalized cross-venue scanner
- execution preview for two-leg trades
- canonical funding and edge model
- annualized funding and potential-return views
- direction-aware BBO capacity for long asks and short bids
- advisory suggested notional at 25% of the weaker leg's visible BBO
- liquidity labels: `deep`, `medium`, `thin`, `toxic`
- fake-liquidity detection and slippage blockers

The suggested notional is advisory, not a profitability guarantee. Users can override it; the planner warns when requested size exceeds visible top-of-book capacity.

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
- historical funding and potential-return charts based on recorded snapshots

### Product Surfaces
- `Fee Rebates` page for GTM narrative
- `Connect Accounts` side panel for multi-venue operations
- `For Agents` page positioning Orbital as a control layer for autonomous capital
- `Portfolio` view focused on health, exposure, risk, and carry

## Technical Highlights

### Stack
- `apps/api` - Go backend
- `apps/web` - React 19 + TypeScript + Tailwind frontend
- `apps/gate-worker` - Cloudflare Worker for closed-beta access
- `SQLite` + embedded migrations + `sqlc`

### Core Models
- `Opportunity` - scan and ranking object
- `ExecutionPlan` - concrete open/close plan
- `Position` - tracked trade lifecycle object

### Current Architecture
- off-chain-first execution architecture
- normalized venue adapters
- canonical hourly-normalized funding model
- BBO-first liquidity model with OI as secondary context
- SQLite-backed paper positions, live positions, sessions, and market history
- browser-held signing; the API does not custody user private keys

## Execution Semantics

Orbital implements the hard parts of two-leg execution:

- riskier leg first
- leg 2 sized from actual leg 1 fill
- at least 50% leg-1 fill required before hedging
- at most 5% residual hedge mismatch
- one residual leg-2 retry before unwind or degraded recovery
- durable live session transitions
- explicit recovery and degraded states in backend and UI

## Live Trading Progress

The constrained Pacifica + Hyperliquid live path is implemented for the closed beta:

- browser-signed, non-custodial venue actions
- account readiness and pre-trade validation
- sequential two-leg open flow
- fill/status confirmation and hedge verification
- durable session recovery across API restarts
- persisted live positions, fills, events, and close outcomes
- per-position reduce-only close
- emergency kill-switch preparation

The next milestone is controlled live validation of partial fills, stale plans, retries, unwind, restart recovery, and close behavior. A universal full-plan simulation gate remains a target control rather than an implemented guarantee.

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
2. premium analytics and cross-venue risk monitoring
3. future agent-ready execution and control layer

The product is designed so that humans can use it today, while autonomous capital workflows can build on top of it later.

## Roadmap

### Product validation
- determine whether BBO-based suggested size is economically useful or should remain a liquidity warning
- measure closed-beta activation, repeated use, plan creation, and live-open completion
- validate opportunity frequency and executable economics before adding venues

### Execution hardening
- validate recovery, unwind, close, and kill-switch paths with controlled live capital
- add venue-specific pre-submit simulation where available
- improve operational alerts, deploy smoke checks, and SQLite backup drills

### Future product surface
- stronger connected-account and signer UX
- more operational portfolio and risk surfaces
- clearer onboarding into venue-linked execution
- delegated browser-held session keys after live execution is stable

Our direction remains the same:

**win on execution quality and risk discipline first, then expand product surface.**

## Run Locally

### Backend

```bash
make api-run
```

This starts the Go API from `apps/api`, runs embedded SQLite migrations automatically, and creates or updates `apps/api/orbital.db`.

### Frontend

```bash
cd apps/web
pnpm install
pnpm dev
```

The frontend runs on Vite and proxies `/api` to the local API on port 8080.

### Build and test

Backend:

```bash
make api-test
make api-build
```

Frontend:

```bash
cd apps/web
pnpm test
pnpm lint
pnpm build
```

## Status

Today Orbital has:

- live Pacifica and Hyperliquid market data
- normalized opportunity discovery and BBO-based sizing
- funding and potential-return projections
- paper execution, monitoring, and analytics
- non-custodial live open, recovery, close, and kill-switch flows
- closed-beta access and production deployment infrastructure

The current focus is product validation and live execution hardening, not basic venue plumbing.
