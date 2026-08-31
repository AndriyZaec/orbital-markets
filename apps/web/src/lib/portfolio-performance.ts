const MS_PER_YEAR = 365 * 24 * 60 * 60 * 1000

export interface PerformancePosition {
  asset: string
  notional: number
  leverage: number
  total_pnl: number
  started_at: string
  opened_at?: string
  completed_at?: string
}

export interface PortfolioPerformance {
  value: number | null
  annualized: boolean
  byAsset: Array<{ asset: string; value: number; annualized: boolean }>
}

interface PerformanceAccumulator {
  pnl: number
  deployedCapital: number
  capitalMilliseconds: number
}

function contribution(position: PerformancePosition, now: number): PerformanceAccumulator | null {
  if (
    !Number.isFinite(position.notional)
    || position.notional <= 0
    || !Number.isFinite(position.leverage)
    || position.leverage <= 0
    || !Number.isFinite(position.total_pnl)
  ) return null

  const openedAt = new Date(position.opened_at || position.started_at).getTime()
  const completedAt = position.completed_at ? new Date(position.completed_at).getTime() : now
  const duration = completedAt - openedAt
  if (!Number.isFinite(openedAt) || !Number.isFinite(completedAt) || duration <= 0) return null

  // `notional` is per leg. Both market-neutral legs reserve their own margin.
  const deployedCapital = (position.notional * 2) / position.leverage
  return {
    pnl: position.total_pnl,
    deployedCapital,
    capitalMilliseconds: deployedCapital * duration,
  }
}

function performance(value: PerformanceAccumulator): { value: number | null; annualized: boolean } {
  if (value.deployedCapital <= 0 || value.capitalMilliseconds <= 0) {
    return { value: null, annualized: false }
  }
  if (value.pnl < 0) {
    return { value: value.pnl / value.deployedCapital, annualized: false }
  }
  return {
    value: (value.pnl * MS_PER_YEAR) / value.capitalMilliseconds,
    annualized: true,
  }
}

export function portfolioPerformance(
  positions: PerformancePosition[],
  now = Date.now(),
): PortfolioPerformance {
  const total: PerformanceAccumulator = { pnl: 0, deployedCapital: 0, capitalMilliseconds: 0 }
  const assets = new Map<string, PerformanceAccumulator>()

  for (const position of positions) {
    const value = contribution(position, now)
    if (!value) continue

    total.pnl += value.pnl
    total.deployedCapital += value.deployedCapital
    total.capitalMilliseconds += value.capitalMilliseconds

    const asset = assets.get(position.asset) ?? { pnl: 0, deployedCapital: 0, capitalMilliseconds: 0 }
    asset.pnl += value.pnl
    asset.deployedCapital += value.deployedCapital
    asset.capitalMilliseconds += value.capitalMilliseconds
    assets.set(position.asset, asset)
  }

  const byAsset = Array.from(assets, ([asset, accumulator]) => {
    const result = performance(accumulator)
    return { asset, ...result }
  }).flatMap((result) => result.value === null ? [] : [{
    asset: result.asset,
    value: result.value,
    annualized: result.annualized,
  }])

  const totalPerformance = performance(total)

  return {
    ...totalPerformance,
    // Input positions are newest-first, so Map insertion order preserves the
    // most recently active assets for the share card.
    byAsset,
  }
}
