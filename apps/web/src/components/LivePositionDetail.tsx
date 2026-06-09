import { useState } from 'react'
import type { LivePosition } from '@/hooks/useLivePositions'
import { useLivePositionDetail, type LiveFillDetail, type LiveEventDetail } from '@/hooks/useLivePositionDetail'
import { useLiveClose } from '@/hooks/useLiveClose'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'

interface Props {
  position: LivePosition
  onClose: () => void
  onRefresh?: () => void
}

function fmtPrice(n: number) {
  if (n >= 1000) return '$' + n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  if (n >= 1) return '$' + n.toFixed(4)
  if (n === 0) return '—'
  return '$' + n.toPrecision(4)
}

function fmtUsd(n: number) {
  if (n >= 1_000_000) return '$' + (n / 1_000_000).toFixed(2) + 'M'
  if (n >= 1_000) return '$' + (n / 1_000).toFixed(2) + 'K'
  return '$' + n.toFixed(2)
}

function fmtPnL(n: number) {
  const sign = n >= 0 ? '+' : ''
  return sign + '$' + Math.abs(n).toFixed(2)
}

function fmtPct(n: number, decimals = 4) {
  return (n * 100).toFixed(decimals) + '%'
}

function fmtHours(h: number) {
  if (h >= 24) return Math.floor(h / 24) + 'd ' + Math.floor(h % 24) + 'h'
  if (h >= 1) return Math.floor(h) + 'h ' + Math.floor((h % 1) * 60) + 'm'
  return Math.floor(h * 60) + 'm'
}

function fmtTime(s: string | undefined) {
  if (!s) return '—'
  return new Date(s).toLocaleString(undefined, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

function stateColor(state: string) {
  switch (state) {
    case 'open': return 'text-green-400'
    case 'degraded': return 'text-orange-400'
    case 'failed': return 'text-red-400'
    case 'closed': return 'text-muted-foreground'
    case 'closing': return 'text-yellow-400'
    default: return 'text-yellow-400'
  }
}

function liqRiskBadge(risk: string) {
  switch (risk) {
    case 'safe': return 'bg-green-500/15 text-green-400'
    case 'elevated': return 'bg-blue-500/15 text-blue-400'
    case 'warning': return 'bg-yellow-500/15 text-yellow-400'
    case 'critical': return 'bg-red-500/15 text-red-400'
    default: return 'bg-white/[0.04] text-muted-foreground'
  }
}

function pnlColor(n: number) {
  if (n > 0) return 'text-green-400'
  if (n < 0) return 'text-red-400'
  return ''
}

const venueLogos: Record<string, string> = { pacifica: pacificaLogo, hyperliquid: hlLogo }

function needsAttention(state: string) {
  return state === 'degraded' || state === 'failed' || state === 'closing'
}

export function LivePositionDetail({ position: pos, onClose, onRefresh }: Props) {
  const { data, loading } = useLivePositionDetail(pos.id)
  const fills = data?.fills ?? []
  const events = data?.events ?? []
  const liveClose = useLiveClose()
  const [confirmClose, setConfirmClose] = useState(false)

  const canClose = pos.state === 'open' || pos.state === 'degraded'
  const isClosing = liveClose.state.phase !== 'idle' && liveClose.state.phase !== 'done' && liveClose.state.phase !== 'error'
  const closeDone = liveClose.state.phase === 'done'

  const handleClose = () => {
    setConfirmClose(false)
    liveClose.closePosition(pos.id)
  }

  const handleDismiss = () => {
    if (closeDone) {
      liveClose.reset()
      onRefresh?.()
    }
    onClose()
  }

  // Extract reason from the last 'complete' event
  const completeEvent = [...events].reverse().find(e => e.event === 'complete')
  const reason = completeEvent?.detail

  return (
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50" onClick={onClose}>
      <div
        className="bg-card border border-border rounded-lg w-[580px] max-h-[90vh] overflow-y-auto shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-5 pt-5 pb-4 flex items-center justify-between border-b border-border">
          <div className="flex items-center gap-3">
            <h2 className="text-lg font-semibold text-foreground">{pos.asset}</h2>
            <Badge variant="outline" className={`text-[11px] ${stateColor(pos.state)}`}>{pos.state}</Badge>
            <div className="flex items-center gap-1 rounded border border-blue-500/30 bg-blue-500/[0.06] px-1.5 py-px">
              <div className="size-1.5 rounded-full bg-blue-400" />
              <span className="text-[9px] text-blue-400 font-medium">LIVE</span>
            </div>
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
              <path d="M11 3L3 11M3 3l8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
            </svg>
          </button>
        </div>

        {/* Reason banner for non-open states */}
        {needsAttention(pos.state) && reason && (
          <div className={`px-5 py-3 border-b ${
            pos.state === 'degraded' ? 'border-orange-500/20 bg-orange-500/[0.04]'
            : 'border-red-500/20 bg-red-500/[0.04]'
          }`}>
            <p className={`text-[11px] font-medium mb-1 ${pos.state === 'degraded' ? 'text-orange-400' : 'text-red-400'}`}>
              {pos.state === 'degraded' ? 'Manual action may be required' : 'Execution failed'}
            </p>
            <p className="text-[10px] text-muted-foreground">{reason}</p>
          </div>
        )}

        {/* Leg Fills */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Leg Fills</p>
          {loading && <p className="text-[10px] text-muted-foreground">Loading...</p>}
          {!loading && fills.length === 0 && (
            <p className="text-[10px] text-muted-foreground">No fills recorded.</p>
          )}
          {fills.length > 0 && (
            <div className="space-y-2">
              {fills.map((f) => (
                <FillCard key={f.id} fill={f} />
              ))}
            </div>
          )}
        </div>

        {/* Spread Health — only for open positions with monitoring data */}
        {pos.state === 'open' && (pos.current_spread !== 0 || pos.entry_spread !== 0) && (
          <div className="px-5 py-4 border-b border-border">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Spread Health</p>
            <div className="grid grid-cols-3 gap-4">
              <InfoItem label="Entry Spread" value={fmtPct(pos.entry_spread)} />
              <InfoItem label="Current Spread" value={fmtPct(pos.current_spread)} warn={pos.current_spread < 0} />
              <InfoItem label="Basis Change" value={fmtPct(pos.basis_change)} warn={pos.basis_change < 0} />
            </div>
          </div>
        )}

        {/* Leg Monitoring — only for open positions with monitoring data */}
        {pos.state === 'open' && (pos.leg1_current_price > 0 || pos.leg2_current_price > 0) && (
          <div className="px-5 py-4 border-b border-border">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Leg Status</p>
            <div className="grid grid-cols-2 gap-3">
              <LegCard label="Leg 1" venue={pos.venue_a} currentPrice={pos.leg1_current_price} liqPrice={pos.leg1_liq_price} liqDist={pos.leg1_liq_dist} liqRisk={pos.leg1_liq_risk} />
              <LegCard label="Leg 2" venue={pos.venue_b} currentPrice={pos.leg2_current_price} liqPrice={pos.leg2_liq_price} liqDist={pos.leg2_liq_dist} liqRisk={pos.leg2_liq_risk} />
            </div>
          </div>
        )}

        {/* PnL — only for open positions */}
        {pos.state === 'open' && (pos.price_pnl !== 0 || pos.funding_pnl !== 0) && (
          <div className="px-5 py-4 border-b border-border">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Profit & Loss</p>
            <div className="grid grid-cols-3 gap-4">
              <div><p className="text-[10px] text-muted-foreground mb-0.5">Price PnL</p><p className={`text-sm font-mono font-medium ${pnlColor(pos.price_pnl)}`}>{fmtPnL(pos.price_pnl)}</p></div>
              <div><p className="text-[10px] text-muted-foreground mb-0.5">Funding PnL</p><p className={`text-sm font-mono font-medium ${pnlColor(pos.funding_pnl)}`}>{fmtPnL(pos.funding_pnl)}</p></div>
              <div><p className="text-[10px] text-muted-foreground mb-0.5">Total PnL</p><p className={`text-sm font-mono font-semibold ${pnlColor(pos.total_pnl)}`}>{fmtPnL(pos.total_pnl)}</p></div>
            </div>
          </div>
        )}

        {/* Position Info */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Position Info</p>
          <div className="grid grid-cols-2 gap-y-3 gap-x-6">
            <InfoItem label="Notional" value={fmtUsd(pos.notional)} />
            <InfoItem label="Leverage" value={`${pos.leverage}x`} />
            <InfoItem label="Venues" value={`${pos.venue_a} / ${pos.venue_b}`} />
            {pos.hold_hours > 0 && <InfoItem label="Hold Time" value={fmtHours(pos.hold_hours)} />}
          </div>
        </div>

        {/* Event Timeline */}
        {events.length > 0 && (
          <div className="px-5 py-4 border-b border-border">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Event Timeline</p>
            <div className="space-y-1.5">
              {events.map((ev) => (
                <EventRow key={ev.id} event={ev} />
              ))}
            </div>
          </div>
        )}

        {/* Close action */}
        {(canClose || isClosing || closeDone) && (
          <div className="px-5 py-4 border-b border-border">
            {/* Confirm prompt */}
            {canClose && !confirmClose && !isClosing && !closeDone && (
              <Button variant="destructive" size="sm" className="w-full" onClick={() => setConfirmClose(true)}>
                Close Position
              </Button>
            )}
            {confirmClose && !isClosing && (
              <div className="flex items-center gap-2">
                <p className="text-[11px] text-muted-foreground flex-1">Close both legs? Your wallet will sign each close order.</p>
                <Button variant="outline" size="xs" onClick={() => setConfirmClose(false)}>Cancel</Button>
                <Button variant="destructive" size="xs" onClick={handleClose}>Confirm</Button>
              </div>
            )}
            {/* Progress */}
            {isClosing && (
              <p className="text-[11px] text-yellow-400">
                {liveClose.state.phase === 'preparing' ? 'Preparing close orders...' :
                 liveClose.state.phase === 'signing' ? `Signing close order ${liveClose.state.submitted + 1} of ${liveClose.state.total} — check your wallet` :
                 `Submitting ${liveClose.state.submitted + 1} of ${liveClose.state.total}...`}
              </p>
            )}
            {/* Result */}
            {closeDone && liveClose.state.failed === 0 && (
              <p className="text-[11px] text-green-400">Close orders submitted successfully.</p>
            )}
            {closeDone && liveClose.state.failed > 0 && (
              <div className="text-[11px] space-y-1">
                <p className="text-yellow-400">{liveClose.state.succeeded} accepted, {liveClose.state.failed} failed</p>
                {liveClose.state.errors.map((e, i) => <p key={i} className="text-red-400/70">{e}</p>)}
              </div>
            )}
            {liveClose.state.phase === 'error' && (
              <p className="text-[11px] text-red-400">{liveClose.state.errors[0]}</p>
            )}
          </div>
        )}

        {/* Timestamps */}
        <div className="px-5 py-4">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Timeline</p>
          <div className="grid grid-cols-2 gap-y-3 gap-x-6">
            <InfoItem label="Started" value={fmtTime(pos.started_at)} />
            <InfoItem label="Opened" value={fmtTime(pos.opened_at)} />
            <InfoItem label="Last Updated" value={fmtTime(pos.updated_at)} />
            {pos.completed_at && <InfoItem label="Completed" value={fmtTime(pos.completed_at)} />}
            {pos.monitor_at && <InfoItem label="Last Monitor" value={fmtTime(pos.monitor_at)} />}
          </div>
        </div>
      </div>
    </div>
  )
}

function FillCard({ fill }: { fill: LiveFillDetail }) {
  const logo = venueLogos[fill.venue]
  const isGood = fill.filled
  const isBad = !fill.accepted || (fill.error && fill.error.length > 0)

  return (
    <div className={`rounded-lg border px-3 py-2.5 ${
      isGood ? 'border-green-500/15 bg-green-500/[0.02]'
      : isBad ? 'border-red-500/15 bg-red-500/[0.02]'
      : 'border-border bg-white/[0.02]'
    }`}>
      <div className="flex items-center gap-2 mb-1.5">
        <span className="text-[10px] text-muted-foreground font-medium">Leg {fill.leg}</span>
        {logo && <img src={logo} alt={fill.venue} className="size-3.5 rounded-sm" />}
        <span className="text-[11px] text-foreground capitalize">{fill.venue}</span>
        <span className={`ml-auto text-[10px] font-medium ${
          fill.filled ? 'text-green-400' : fill.accepted ? 'text-yellow-400' : 'text-red-400'
        }`}>
          {fill.filled ? 'Filled' : fill.accepted ? 'Accepted' : 'Rejected'}
        </span>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-[10px]">
        <span><span className="text-muted-foreground">Side: </span><span className={`font-medium ${fill.side === 'long' ? 'text-green-400' : 'text-red-400'}`}>{fill.side}</span></span>
        <span><span className="text-muted-foreground">Size: </span><span className="font-mono text-foreground">{fill.filled_amount > 0 ? fill.filled_amount.toPrecision(4) : '—'}</span></span>
        <span><span className="text-muted-foreground">Req: </span><span className="font-mono text-foreground">{fill.requested_amount > 0 ? fill.requested_amount.toPrecision(4) : '—'}</span></span>
        <span><span className="text-muted-foreground">Avg Price: </span><span className="font-mono text-foreground">{fill.avg_fill_price > 0 ? fmtPrice(fill.avg_fill_price) : '—'}</span></span>
        <span><span className="text-muted-foreground">Fill: </span><span className="font-mono text-foreground">{fmtPct(fill.fill_ratio, 1)}</span></span>
        {fill.fee > 0 && <span><span className="text-muted-foreground">Fee: </span><span className="font-mono text-foreground">${fill.fee.toFixed(4)}</span></span>}
      </div>
      {fill.order_id && (
        <p className="text-[9px] text-muted-foreground/50 font-mono mt-1 truncate">OID: {fill.order_id}</p>
      )}
      {fill.client_order_id && (
        <p className="text-[9px] text-muted-foreground/50 font-mono truncate">CLOID: {fill.client_order_id}</p>
      )}
      {fill.error && (
        <p className="text-[10px] text-red-400/70 mt-1">{fill.error}</p>
      )}
    </div>
  )
}

function EventRow({ event: ev }: { event: LiveEventDetail }) {
  const isComplete = ev.event === 'complete'
  const isError = ev.state === 'degraded' || ev.state === 'failed'

  return (
    <div className="flex items-start gap-2 text-[10px]">
      <span className="text-muted-foreground/60 font-mono shrink-0 w-[130px]">{fmtTime(ev.at)}</span>
      <span className={`font-medium shrink-0 w-[100px] ${
        isComplete && isError ? 'text-red-400' : isComplete ? 'text-green-400' : 'text-foreground'
      }`}>{ev.event}</span>
      {ev.detail && <span className="text-muted-foreground truncate">{ev.detail}</span>}
    </div>
  )
}

function LegCard({ label, venue, currentPrice, liqPrice, liqDist, liqRisk }: {
  label: string; venue: string; currentPrice: number; liqPrice: number; liqDist: number; liqRisk: string
}) {
  const logo = venueLogos[venue]
  return (
    <div className="rounded-lg border border-border bg-white/[0.02] px-3 py-3">
      <div className="flex items-center gap-2 mb-2.5">
        <span className="text-[10px] text-muted-foreground font-medium">{label}</span>
        {logo && <img src={logo} alt={venue} className="size-4 rounded-sm" />}
        <span className="text-xs text-foreground capitalize">{venue}</span>
      </div>
      <div className="flex flex-col gap-1.5 text-[11px]">
        <div className="flex justify-between"><span className="text-muted-foreground">Price</span><span className="font-mono text-foreground">{fmtPrice(currentPrice)}</span></div>
        <div className="flex justify-between"><span className="text-muted-foreground">Liq Price</span><span className="font-mono text-foreground">{fmtPrice(liqPrice)}</span></div>
        <div className="flex justify-between"><span className="text-muted-foreground">Liq Dist</span><span className="font-mono text-foreground">{liqDist > 0 ? fmtPct(liqDist, 2) : '—'}</span></div>
        {liqRisk && (
          <div className="flex justify-between items-center">
            <span className="text-muted-foreground">Risk</span>
            <span className={`text-[10px] font-medium px-1.5 py-0.5 rounded ${liqRiskBadge(liqRisk)}`}>{liqRisk}</span>
          </div>
        )}
      </div>
    </div>
  )
}

function InfoItem({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div>
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <p className={`text-sm font-mono ${warn ? 'text-orange-400' : 'text-foreground'}`}>{value}</p>
    </div>
  )
}
