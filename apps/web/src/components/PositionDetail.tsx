import type { PaperPosition, Fill, Event } from '@/hooks/usePaperPositions'
import { Badge } from '@/components/ui/badge'

interface Props {
  position: PaperPosition
  onClose: () => void
}

function fmtPrice(n: number) {
  if (n >= 1000) return n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  if (n >= 1) return n.toFixed(4)
  return n.toPrecision(4)
}

function fmtPnL(n: number) {
  const sign = n >= 0 ? '+' : ''
  return sign + n.toFixed(4)
}

function fmtTime(s: string) {
  return new Date(s).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function stateColor(state: string) {
  switch (state) {
    case 'open': return 'text-green-400'
    case 'degraded': case 'failed': return 'text-red-400'
    case 'closed': return 'text-muted-foreground'
    default: return 'text-yellow-400'
  }
}

function closeReasonText(reason: string) {
  const map: Record<string, string> = {
    manual: 'Manual',
    edge_collapse: 'Edge Collapse',
    degraded: 'Degraded',
    max_duration: 'Max Duration',
    liquidation_risk: 'Liquidation Risk',
  }
  return map[reason] ?? reason
}

function closeReasonColor(reason: string) {
  const map: Record<string, string> = {
    liquidation_risk: 'text-red-400',
    degraded: 'text-orange-400',
    edge_collapse: 'text-yellow-400',
  }
  return map[reason] ?? ''
}

function liqDistColor(risk: Fill['liq_risk']) {
  switch (risk) {
    case 'safe': return 'text-green-400'
    case 'elevated': return 'text-blue-400'
    case 'warning': return 'text-yellow-400'
    case 'critical': return 'text-red-400'
    default: return ''
  }
}

function liqRiskBadge(risk: Fill['liq_risk']) {
  switch (risk) {
    case 'safe': return 'bg-green-500/15 text-green-400'
    case 'elevated': return 'bg-blue-500/15 text-blue-400'
    case 'warning': return 'bg-yellow-500/15 text-yellow-400'
    case 'critical': return 'bg-red-500/15 text-red-400'
    default: return 'bg-white/[0.04] text-muted-foreground'
  }
}

export function PositionDetail({ position: pos, onClose }: Props) {
  return (
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50" onClick={onClose}>
      <div
        className="bg-card border border-border rounded-lg w-[560px] max-h-[90vh] overflow-y-auto shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-5 pt-5 pb-4 flex items-center justify-between border-b border-border">
          <div className="flex items-center gap-3">
            <h2 className="text-lg font-semibold text-foreground">{pos.asset}</h2>
            <Badge variant="outline" className={`text-[11px] ${stateColor(pos.state)}`}>{pos.state}</Badge>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors"
          >
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
              <path d="M11 3L3 11M3 3l8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
            </svg>
          </button>
        </div>

        {/* Fills */}
        <div className="px-5 py-4 border-b border-border flex flex-col gap-2.5">
          {pos.leg_1_fill && <FillCard fill={pos.leg_1_fill} label="Leg 1" />}
          {pos.leg_2_fill && <FillCard fill={pos.leg_2_fill} label="Leg 2" />}
          {!pos.leg_1_fill && !pos.leg_2_fill && (
            <p className="text-sm text-muted-foreground">No fills recorded</p>
          )}
        </div>

        {/* Metrics */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Position Metrics</p>
          <div className="grid grid-cols-2 gap-y-3 gap-x-6">
            <InfoItem label="Target Notional" value={`$${pos.target_notional.toFixed(2)}`} />
            <InfoItem label="Leverage" value={`${pos.leverage.leverage.toFixed(0)}x (${pos.leverage.effective_leverage.toFixed(1)}x eff.)`} />
            <InfoItem label="Margin Required" value={`$${pos.leverage.margin_required.toFixed(2)}`} />
            <InfoItem label="Hedge Mismatch" value={`${(pos.hedge_mismatch * 100).toFixed(1)}%`} />
            <InfoItem label="Entry Cost" value={`${(pos.entry_spread * 100).toFixed(4)}%`} />
            <InfoItem label="Ann. Gross Edge" value={`${(pos.current_spread * 100).toFixed(2)}%`} />
            <InfoItem
              label="Price P&L"
              value={fmtPnL(pos.price_pnl)}
              color={pos.price_pnl >= 0 ? 'text-green-400' : 'text-red-400'}
            />
            <InfoItem
              label="Funding P&L"
              value={fmtPnL(pos.funding_pnl)}
              color={pos.funding_pnl >= 0 ? 'text-green-400' : 'text-red-400'}
            />
            <InfoItem
              label="Total P&L"
              value={fmtPnL(pos.total_pnl)}
              color={pos.total_pnl >= 0 ? 'text-green-400' : 'text-red-400'}
            />
            <InfoItem label="Entry Basis" value={`${(pos.entry_basis * 100).toFixed(4)}%`} />
            <InfoItem label="Current Basis" value={`${(pos.current_basis * 100).toFixed(4)}%`} />
            <InfoItem
              label="Basis Change"
              value={`${pos.basis_change >= 0 ? '+' : ''}${(pos.basis_change * 100).toFixed(4)}%`}
              color={pos.basis_change >= 0 ? 'text-green-400' : 'text-red-400'}
            />
            {pos.state === 'closed' && (
              <InfoItem
                label="Realized P&L"
                value={fmtPnL(pos.realized_pnl)}
                color={pos.realized_pnl >= 0 ? 'text-green-400' : 'text-red-400'}
              />
            )}
            {pos.close_reason && (
              <InfoItem
                label="Close Reason"
                value={closeReasonText(pos.close_reason)}
                color={closeReasonColor(pos.close_reason)}
              />
            )}
          </div>
        </div>

        {/* Event Timeline */}
        <div className="px-5 py-4">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">
            Timeline ({pos.events.length})
          </p>
          <div className="flex flex-col gap-0">
            {pos.events.map((event, i) => (
              <TimelineEvent key={i} event={event} isLast={i === pos.events.length - 1} />
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}

function FillCard({ fill, label }: { fill: Fill; label: string }) {
  const isLong = fill.side === 'long'
  const sideColor = isLong ? 'text-green-400' : 'text-red-400'
  const borderColor = isLong ? 'border-green-500/20' : 'border-red-500/20'

  return (
    <div className={`rounded-md bg-white/[0.02] border ${borderColor} px-4 py-3`}>
      <div className="flex items-center justify-between mb-2.5">
        <span className="text-sm font-medium text-foreground">{label}</span>
        <span className={`text-[11px] font-semibold uppercase ${sideColor}`}>{fill.side}</span>
      </div>
      <div className="grid grid-cols-4 gap-2 text-sm">
        <div>
          <p className="text-[11px] text-muted-foreground">Venue</p>
          <p className="font-medium capitalize text-foreground">{fill.venue}</p>
        </div>
        <div>
          <p className="text-[11px] text-muted-foreground">Fill Price</p>
          <p className="font-mono text-foreground">${fmtPrice(fill.fill_price)}</p>
        </div>
        <div>
          <p className="text-[11px] text-muted-foreground">Current</p>
          <p className="font-mono text-foreground">{fill.current_price ? '$' + fmtPrice(fill.current_price) : '—'}</p>
        </div>
        <div>
          <p className="text-[11px] text-muted-foreground">Fill Ratio</p>
          <p className="font-mono text-foreground">{((fill.filled_size / fill.target_size) * 100).toFixed(1)}%</p>
        </div>
      </div>
      <div className="grid grid-cols-4 gap-2 text-sm mt-2">
        <div>
          <p className="text-[11px] text-muted-foreground">Funding (h)</p>
          <p className="font-mono text-foreground">{(fill.current_funding * 100).toFixed(4)}%</p>
        </div>
        <div>
          <p className="text-[11px] text-muted-foreground">Accum.</p>
          <p className={`font-mono ${fill.accum_funding >= 0 ? 'text-green-400' : 'text-red-400'}`}>{fmtPnL(fill.accum_funding)}</p>
        </div>
        <div>
          <p className="text-[11px] text-muted-foreground">Price P&L</p>
          <p className={`font-mono ${fill.leg_price_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>{fmtPnL(fill.leg_price_pnl)}</p>
        </div>
        <div>
          <p className="text-[11px] text-muted-foreground">Next Fund.</p>
          <p className="font-mono text-[11px] text-muted-foreground">{fill.next_funding_at ? new Date(fill.next_funding_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) : '—'}</p>
        </div>
      </div>
      {fill.liquidation_price > 0 && (
        <div className="grid grid-cols-3 gap-2 text-sm pt-2 border-t border-border/50 mt-2">
          <div>
            <p className="text-[11px] text-muted-foreground">Liq. Price</p>
            <p className="font-mono text-foreground">${fmtPrice(fill.liquidation_price)}</p>
          </div>
          <div>
            <p className="text-[11px] text-muted-foreground">Liq. Distance</p>
            <p className={`font-mono ${liqDistColor(fill.liq_risk)}`}>
              {(fill.liquidation_dist * 100).toFixed(1)}%
            </p>
          </div>
          <div>
            <p className="text-[11px] text-muted-foreground">Liq. Risk</p>
            <span className={`inline-block text-[11px] font-medium px-1.5 py-0.5 rounded ${liqRiskBadge(fill.liq_risk)}`}>
              {fill.liq_risk || 'n/a'}
            </span>
          </div>
        </div>
      )}
    </div>
  )
}

function TimelineEvent({ event, isLast }: { event: Event; isLast: boolean }) {
  const dotColor = stateColor(event.to_state)

  return (
    <div className="flex gap-3 items-start">
      <div className="flex flex-col items-center">
        <div className={`size-2 rounded-full mt-1.5 ${dotColor.replace('text-', 'bg-')}`} />
        {!isLast && <div className="w-px flex-1 bg-border min-h-4" />}
      </div>
      <div className="pb-3 flex-1 min-w-0">
        <div className="flex items-baseline gap-2">
          <span className={`text-[11px] font-medium ${dotColor}`}>{event.to_state}</span>
          <span className="text-[11px] text-muted-foreground">{fmtTime(event.at)}</span>
        </div>
        <p className="text-[11px] text-muted-foreground truncate">{event.reason}</p>
      </div>
    </div>
  )
}

function InfoItem({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div>
      <p className="text-[11px] text-muted-foreground">{label}</p>
      <p className={`text-sm font-mono ${color ?? 'text-foreground'}`}>{value}</p>
    </div>
  )
}
