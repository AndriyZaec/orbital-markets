<p align="center">
  <img src="apps/web/src/assets/hero.png" alt="Orbital Markets" width="1200" />
</p>

# Orbital Market

Orbital Market is an execution copilot for hedged carry trades.

It helps traders discover funding opportunities across venues, size them based on real execution quality, open the hedge in a safer way, and manage the position until exit.

This is not a passive yield vault.
This is not a generic trading terminal.
This is not a pure arbitrage dashboard.

Orbital is built around one hard problem: turning attractive funding spreads into trades that are actually executable and worth holding.

## Why This Exists

Most funding-rate tools stop at signal discovery.

They tell you where the APY looks high, but not:

- whether the trade can be entered cleanly at size
- how much slippage and inventory risk you are taking
- how long you need to hold to break even
- whether the hedge is still healthy after entry
- when you should exit before the thesis breaks

Orbital is designed to close that gap.

## Product

**Three layers. One execution loop. Real carry trades.**

### Market Intelligence
Funding spread discovery.
Execution-aware edge ranking.
Liquidity and slippage checks.

### Execution Engine
Two-leg execution plans.
Recommended notional sizing.
Hedge integrity controls.

### Monitoring & Analytics
Price, funding, and total PnL.
Basis and liquidation tracking.
Paper execution and post-trade analytics.

## Core Workflow

1. Scan venues for funding and basis opportunities.
2. Rank opportunities by execution-aware edge, not headline APY alone.
3. Build a fresh execution plan from live venue data.
4. Validate slippage, liquidity quality, and hedge viability.
5. Execute two legs with guarded sequential logic.
6. Monitor funding carry, basis drift, liquidation proximity, and exit conditions.

## What Makes Orbital Different

Orbital does not optimize for the prettiest spread table.

It optimizes for the full trade lifecycle:

- signal quality
- sizing quality
- safer hedge entry
- degraded-state handling
- break-even awareness
- real outcome analytics

In one line:

**Orbital turns funding opportunities into structured, risk-aware execution workflows.**

## Current Capabilities

### Scanner
- Pacifica and Hyperliquid venue adapters
- normalized market snapshots
- funding spread detection
- annualized gross edge and estimated net edge
- execution-aware ranking

### Execution Preview
- fresh execution plan generation
- two-leg trade preview
- slippage and entry cost bounds
- leverage-aware margin and exposure view
- recommended notional sizing

### Liquidity Intelligence
- BBO-first sizing model
- explicit liquidity labels: `deep`, `medium`, `thin`, `toxic`
- fake-liquidity suspicion signals
- hard slippage blockers and warnings

### Paper Execution
- execution state machine
- partial-fill-aware logic
- retry, unwind, and degraded handling
- per-leg tracking
- auto-close on degraded state, edge collapse, max duration, and critical liquidation risk

### Monitoring
- price PnL, funding PnL, total PnL
- basis tracking
- leverage and gross exposure
- estimated liquidation price and liquidation distance
- per-leg funding and PnL tracking

### Analytics
- DB-backed paper analytics
- break-even tracking
- risk-tier and asset breakdowns
- close-reason analysis
- research history for replay and iteration

## Technical Design

### Architecture
- `apps/api` — Go backend, scanner, paper execution, analytics, persistence
- `apps/web` — React frontend for opportunities, plan preview, paper positions, and analytics
- `packages/shared` — shared types/utilities
- `services/` — reserved for additional execution and market-data services as the system evolves

### Backend Principles
- off-chain-first execution architecture
- normalized venue adapters
- canonical funding and edge math
- BBO-first liquidity model
- SQLite-backed research and analytics layer
- paper execution before live execution

### Data Model
The product is organized around three core objects:

- `Opportunity` — scan and ranking object
- `ExecutionPlan` — concrete open/close plan
- `Position` — tracked trade lifecycle object

## Execution Semantics

Orbital currently models execution with the following rules:

- riskier leg first
- leg 2 sized from actual leg 1 fill
- minimum hedgeable fill threshold
- acceptable hedge mismatch band
- retry once -> unwind -> degraded recovery policy
- explicit degraded state in backend and UI

This gives the system a realistic execution model before live capital is introduced.

## Risk Model

Orbital assumes that funding carry is not enough by itself.

The engine explicitly models and surfaces:

- slippage
- fake liquidity
- basis drift
- partial fill risk
- leverage and liquidation proximity
- break-even time vs holding time

The product is designed to answer a hard but practical question:

**Is this carry trade actually worth entering, sizing, and holding?**

## Business Value

For users, Orbital provides:

- better filtering of attractive but bad trades
- safer execution on fragile liquidity
- clear trade sizing instead of blind notional guesses
- live view into whether the carry thesis is still intact
- analytics that improve future execution decisions

For the business, Orbital can naturally monetize at the execution layer:

- execution fees on opened trades
- premium analytics and monitoring
- later, managed or semi-automated capital rotation workflows

## Project Status

Orbital already has a real vertical slice:

- live market ingestion
- normalized scanner
- execution preview
- paper trading engine
- monitoring and analytics

The next major step is constrained live execution plumbing for supported venues.

## Philosophy

Orbital is not trying to be a magical arbitrage bot.

It is being built as a serious operator tool for hedged carry trading:

- find better trades
- size them rationally
- execute them with more structure
- manage them with less guesswork

## Repository Notes

This repo is under active development.
