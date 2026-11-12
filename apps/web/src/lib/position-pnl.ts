import type { LivePosition } from '../hooks/useLivePositions'

// Venue accounting components belong in the shared PnL presentation, not in
// venue-specific rows. The API total already includes any known accrued funding.
export function positionPnL(position: Pick<LivePosition, 'funding_pnl' | 'funding_pnl_source' | 'total_pnl' | 'phoenix_pending_funding'>) {
  if (position.funding_pnl_source !== 'realized') {
    return { funding: position.funding_pnl, total: position.total_pnl }
  }
  const accrued = position.phoenix_pending_funding?.amount_usd
  if (accrued === null) return { funding: null, total: null }
  return { funding: position.funding_pnl + (accrued ?? 0), total: position.total_pnl }
}
