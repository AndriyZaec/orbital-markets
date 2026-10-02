const MS_PER_YEAR = 365 * 24 * 60 * 60 * 1000

export interface ClosedPositionPerformanceInput {
  notional: number
  leverage: number
  total_pnl: number
  hold_hours: number
  started_at: string
  opened_at?: string
  completed_at?: string
}

export interface ClosedPositionShareMetrics {
  heroLabel: 'APR' | 'ROI'
  heroValue: number
  roi: number
  holdDuration: string
}

export function closedPositionShareMetrics(position: ClosedPositionPerformanceInput): ClosedPositionShareMetrics | null {
  if (!Number.isFinite(position.notional) || position.notional <= 0
    || !Number.isFinite(position.leverage) || position.leverage <= 0
    || !Number.isFinite(position.total_pnl)) return null

  const deployedCapital = (position.notional * 2) / position.leverage
  const roi = position.total_pnl / deployedCapital
  const openedAt = new Date(position.opened_at || position.started_at).getTime()
  const completedAt = position.completed_at ? new Date(position.completed_at).getTime() : NaN
  const timestampHours = (completedAt - openedAt) / (60 * 60 * 1000)
  const holdHours = Number.isFinite(timestampHours) && timestampHours > 0 ? timestampHours : position.hold_hours
  if (!Number.isFinite(holdHours) || holdHours <= 0) return null

  const annualized = roi * (MS_PER_YEAR / (holdHours * 60 * 60 * 1000))
  return {
    heroLabel: position.total_pnl >= 0 ? 'APR' : 'ROI',
    heroValue: position.total_pnl >= 0 ? annualized : roi,
    roi,
    holdDuration: formatHoldDuration(holdHours),
  }
}

function formatHoldDuration(hours: number): string {
  if (hours >= 24) {
    const days = Math.floor(hours / 24)
    const remainingHours = Math.floor(hours % 24)
    return remainingHours > 0 ? `${days}d ${remainingHours}h` : `${days}d`
  }
  if (hours >= 1) return `${Math.floor(hours)}h`
  return `${Math.max(1, Math.floor(hours * 60))}m`
}
