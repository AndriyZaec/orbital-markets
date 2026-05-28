import type { LivePosition } from '@/hooks/useLivePositions'
import { Badge } from '@/components/ui/badge'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'

interface Props {
  position: LivePosition
  onClose: () => void
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
    case 'pending': case 'closing': return 'text-yellow-400'
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

export function LivePositionDetail({ position: pos, onClose }: Props) {
  const basisDeteriorating = pos.basis_change < 0

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
            <div className="flex items-center gap-1 rounded border border-blue-500/30 bg-blue-500/[0.06] px-1.5 py-px">
              <div className="size-1.5 rounded-full bg-blue-400" />
              <span className="text-[9px] text-blue-400 font-medium">LIVE</span>
            </div>
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

        {/* Spread Health */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Spread Health</p>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <p className="text-[10px] text-muted-foreground mb-0.5">Entry Spread</p>
              <p className="text-sm font-mono text-foreground">{fmtPct(pos.entry_spread)}</p>
            </div>
            <div>
              <p className="text-[10px] text-muted-foreground mb-0.5">Current Spread</p>
              <p className={`text-sm font-mono ${pos.current_spread < 0 ? 'text-red-400' : 'text-foreground'}`}>{fmtPct(pos.current_spread)}</p>
            </div>
            <div>
              <p className="text-[10px] text-muted-foreground mb-0.5">Basis Change</p>
              <p className={`text-sm font-mono ${basisDeteriorating ? 'text-red-400' : pnlColor(pos.basis_change)}`}>{fmtPct(pos.basis_change)}</p>
            </div>
          </div>
        </div>

        {/* Leg Summary */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Leg Summary</p>
          <div className="grid grid-cols-2 gap-3">
            <LegCard
              label="Leg 1"
              venue={pos.venue_a}
              currentPrice={pos.leg1_current_price}
              liqPrice={pos.leg1_liq_price}
              liqDist={pos.leg1_liq_dist}
              liqRisk={pos.leg1_liq_risk}
            />
            <LegCard
              label="Leg 2"
              venue={pos.venue_b}
              currentPrice={pos.leg2_current_price}
              liqPrice={pos.leg2_liq_price}
              liqDist={pos.leg2_liq_dist}
              liqRisk={pos.leg2_liq_risk}
            />
          </div>
        </div>

        {/* PnL */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Profit & Loss</p>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <p className="text-[10px] text-muted-foreground mb-0.5">Price PnL</p>
              <p className={`text-sm font-mono font-medium ${pnlColor(pos.price_pnl)}`}>{fmtPnL(pos.price_pnl)}</p>
            </div>
            <div>
              <p className="text-[10px] text-muted-foreground mb-0.5">Funding PnL</p>
              <p className={`text-sm font-mono font-medium ${pnlColor(pos.funding_pnl)}`}>{fmtPnL(pos.funding_pnl)}</p>
            </div>
            <div>
              <p className="text-[10px] text-muted-foreground mb-0.5">Total PnL</p>
              <p className={`text-sm font-mono font-semibold ${pnlColor(pos.total_pnl)}`}>{fmtPnL(pos.total_pnl)}</p>
            </div>
          </div>
        </div>

        {/* Position Info */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Position Info</p>
          <div className="grid grid-cols-2 gap-y-3 gap-x-6">
            <InfoItem label="Notional" value={fmtUsd(pos.notional)} />
            <InfoItem label="Leverage" value={`${pos.leverage}x`} />
            <InfoItem label="Hedge Mismatch" value={fmtPct(pos.hedge_mismatch)} warn={pos.hedge_mismatch > 0.02} />
            <InfoItem label="Hold Time" value={pos.hold_hours > 0 ? fmtHours(pos.hold_hours) : '—'} />
          </div>
        </div>

        {/* Timestamps */}
        <div className="px-5 py-4">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Timeline</p>
          <div className="grid grid-cols-2 gap-y-3 gap-x-6">
            <InfoItem label="Started" value={fmtTime(pos.started_at)} />
            <InfoItem label="Opened" value={fmtTime(pos.opened_at)} />
            <InfoItem label="Last Updated" value={fmtTime(pos.updated_at)} />
            {pos.completed_at && <InfoItem label="Completed" value={fmtTime(pos.completed_at)} />}
          </div>
        </div>
      </div>
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
        <div className="flex justify-between">
          <span className="text-muted-foreground">Price</span>
          <span className="font-mono text-foreground">{fmtPrice(currentPrice)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-muted-foreground">Liq Price</span>
          <span className="font-mono text-foreground">{fmtPrice(liqPrice)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-muted-foreground">Liq Dist</span>
          <span className="font-mono text-foreground">{liqDist > 0 ? fmtPct(liqDist, 2) : '—'}</span>
        </div>
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
