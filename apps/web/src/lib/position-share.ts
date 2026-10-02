const MS_PER_YEAR = 365 * 24 * 60 * 60 * 1000

export type PositionShareKind = 'active' | 'closed'

export interface PositionPerformanceInput {
  notional: number
  leverage: number
  total_pnl: number
  hold_hours: number
  started_at: string
  opened_at?: string
  completed_at?: string
}

export interface PositionShareMetrics {
  title: 'Active Position' | 'Closed Position'
  heroLabel: 'APR' | 'ROI' | 'CURRENT APR' | 'CURRENT ROI'
  heroValue: number
  roi: number
  holdDuration: string
  durationLabel: 'ACTIVE FOR' | 'HOLD'
  pnlLabel: 'UNREALIZED PNL' | 'REALIZED PNL'
  pnlValue: number
  deployedCapital: number
}

export function positionShareMetrics(
  position: PositionPerformanceInput,
  kind: PositionShareKind,
  now = Date.now(),
): PositionShareMetrics | null {
  if (!Number.isFinite(position.notional) || position.notional <= 0
    || !Number.isFinite(position.leverage) || position.leverage <= 0
    || !Number.isFinite(position.total_pnl)) return null

  const deployedCapital = (position.notional * 2) / position.leverage
  const roi = position.total_pnl / deployedCapital
  const openedAt = new Date(position.opened_at || position.started_at).getTime()
  const endpoint = kind === 'active'
    ? now
    : position.completed_at ? new Date(position.completed_at).getTime() : Number.NaN
  const timestampHours = kind === 'closed' && !position.completed_at
    ? Number.NaN
    : (endpoint - openedAt) / (60 * 60 * 1000)
  const holdHours = Number.isFinite(timestampHours) && timestampHours > 0 ? timestampHours : position.hold_hours
  if (!Number.isFinite(holdHours) || holdHours <= 0) return null

  const annualized = roi * (MS_PER_YEAR / (holdHours * 60 * 60 * 1000))
  return {
    title: kind === 'active' ? 'Active Position' : 'Closed Position',
    heroLabel: kind === 'active'
      ? position.total_pnl >= 0 ? 'CURRENT APR' : 'CURRENT ROI'
      : position.total_pnl >= 0 ? 'APR' : 'ROI',
    heroValue: position.total_pnl >= 0 ? annualized : roi,
    roi,
    holdDuration: formatHoldDuration(holdHours),
    durationLabel: kind === 'active' ? 'ACTIVE FOR' : 'HOLD',
    pnlLabel: kind === 'active' ? 'UNREALIZED PNL' : 'REALIZED PNL',
    pnlValue: position.total_pnl,
    deployedCapital,
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
