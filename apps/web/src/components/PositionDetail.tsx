import type { PaperPosition, Fill, Event } from '@/hooks/usePaperPositions'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'

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

export function PositionDetail({ position: pos, onClose }: Props) {
  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50" onClick={onClose}>
      <div
        className="bg-card border border-border rounded-lg w-[560px] max-h-[90vh] overflow-y-auto shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-6 pt-5 pb-4 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <h2 className="text-lg font-semibold">{pos.asset}</h2>
            <Badge variant="outline" className={stateColor(pos.state)}>{pos.state}</Badge>
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground text-lg">×</button>
        </div>

        <Separator />

        {/* Fills */}
        <div className="px-6 py-4 flex flex-col gap-3">
          {pos.leg_1_fill && <FillCard fill={pos.leg_1_fill} label="Leg 1" />}
          {pos.leg_2_fill && <FillCard fill={pos.leg_2_fill} label="Leg 2" />}
          {!pos.leg_1_fill && !pos.leg_2_fill && (
            <p className="text-sm text-muted-foreground">No fills recorded</p>
          )}
        </div>

        <Separator />

        {/* Metrics */}
        <div className="px-6 py-4">
          <h3 className="text-sm font-medium text-muted-foreground mb-3">Position Metrics</h3>
          <div className="grid grid-cols-2 gap-y-3 gap-x-6">
            <InfoItem label="Target Notional" value={`$${pos.target_notional.toFixed(2)}`} />
            <InfoItem label="Hedge Mismatch" value={`${(pos.hedge_mismatch * 100).toFixed(1)}%`} />
            <InfoItem label="Entry Cost" value={`${(pos.entry_spread * 100).toFixed(4)}%`} />
            <InfoItem label="Annualized Gross Edge" value={`${(pos.current_spread * 100).toFixed(2)}%`} />
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
              <InfoItem label="Close Reason" value={pos.close_reason} />
            )}
          </div>
        </div>

        <Separator />

        {/* Event Timeline */}
        <div className="px-6 py-4">
          <h3 className="text-sm font-medium text-muted-foreground mb-3">
            Event Timeline ({pos.events.length})
          </h3>
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
  const sideColor = fill.side === 'long' ? 'text-green-400' : 'text-red-400'
  const sideBorder = fill.side === 'long' ? 'border-green-500/20' : 'border-red-500/20'

  return (
    <Card className={`bg-muted/50 border ${sideBorder}`}>
      <CardHeader className="pb-2 pt-3 px-4">
        <CardTitle className="text-sm font-medium flex items-center justify-between">
          <span>{label}</span>
          <span className={`text-xs font-semibold uppercase ${sideColor}`}>{fill.side}</span>
        </CardTitle>
      </CardHeader>
      <CardContent className="px-4 pb-3 flex flex-col gap-2">
        <div className="grid grid-cols-4 gap-2 text-sm">
          <div>
            <p className="text-xs text-muted-foreground">Venue</p>
            <p className="font-medium capitalize">{fill.venue}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Fill Price</p>
            <p className="font-mono">${fmtPrice(fill.fill_price)}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Current Price</p>
            <p className="font-mono">{fill.current_price ? '$' + fmtPrice(fill.current_price) : '—'}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Fill Ratio</p>
            <p className="font-mono">{((fill.filled_size / fill.target_size) * 100).toFixed(1)}%</p>
          </div>
        </div>
        <div className="grid grid-cols-4 gap-2 text-sm">
          <div>
            <p className="text-xs text-muted-foreground">Funding (h)</p>
            <p className="font-mono">{(fill.current_funding * 100).toFixed(4)}%</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Accum. Funding</p>
            <p className={`font-mono ${fill.accum_funding >= 0 ? 'text-green-400' : 'text-red-400'}`}>{fmtPnL(fill.accum_funding)}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Price P&L</p>
            <p className={`font-mono ${fill.leg_price_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>{fmtPnL(fill.leg_price_pnl)}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Next Funding</p>
            <p className="font-mono text-xs">{fill.next_funding_at ? new Date(fill.next_funding_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) : '—'}</p>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function TimelineEvent({ event, isLast }: { event: Event; isLast: boolean }) {
  const dotColor = stateColor(event.to_state)

  return (
    <div className="flex gap-3 items-start">
      {/* Dot + line */}
      <div className="flex flex-col items-center">
        <div className={`size-2 rounded-full mt-1.5 ${dotColor.replace('text-', 'bg-')}`} />
        {!isLast && <div className="w-px flex-1 bg-border min-h-4" />}
      </div>

      {/* Content */}
      <div className="pb-3 flex-1 min-w-0">
        <div className="flex items-baseline gap-2">
          <span className={`text-xs font-medium ${dotColor}`}>{event.to_state}</span>
          <span className="text-xs text-muted-foreground">{fmtTime(event.at)}</span>
        </div>
        <p className="text-xs text-muted-foreground truncate">{event.reason}</p>
      </div>
    </div>
  )
}

function InfoItem({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className={`text-sm font-mono ${color ?? ''}`}>{value}</p>
    </div>
  )
}
