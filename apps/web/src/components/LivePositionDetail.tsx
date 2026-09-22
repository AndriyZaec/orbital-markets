import { useEffect, useState } from 'react'
import type { LivePosition } from '@/hooks/useLivePositions'
import { useLivePositionDetail, type LiveFillDetail, type LiveEventDetail } from '@/hooks/useLivePositionDetail'
import { useLiveClose, type CloseOutcome } from '@/hooks/useLiveClose'
import { canRequestLiveClose, hasActionableRecordedFills } from '@/lib/degraded-execution'
import { monitoredLegVenues } from '@/lib/live-position-monitoring'
import { formatSignedUsdPnL } from '@/lib/pnl-format'
import { venueTradeUrl } from '@/lib/venue-links'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { AssetIcon } from '@/components/AssetIcon'
import { ExternalLinkIcon } from 'lucide-react'
import { venueMetadata } from '@/lib/venue-metadata'

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

function fmtEventTime(s: string) {
  return new Date(s).toLocaleTimeString(undefined, {
    hour: '2-digit', minute: '2-digit', second: '2-digit',
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

function needsAttention(state: string) {
  return state === 'degraded' || state === 'failed' || state === 'closing'
}

export function LivePositionDetail({ position: pos, onClose, onRefresh }: Props) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70" onClick={onClose}>
      <div className="h-[90vh] w-[580px] overflow-hidden rounded-lg border border-border bg-card shadow-2xl" onClick={(event) => event.stopPropagation()}>
        <LivePositionContent position={pos} onDismiss={onClose} onRefresh={onRefresh} />
      </div>
    </div>
  )
}

export function LivePositionPanel({ position: pos, onClose, onRefresh }: Props) {
  return (
    <aside className="flex h-full w-[360px] shrink-0 border-l border-border bg-card">
      <LivePositionContent position={pos} onDismiss={onClose} onRefresh={onRefresh} compact />
    </aside>
  )
}

function LivePositionContent({ position, onDismiss, onRefresh, compact = false }: {
  position: LivePosition
  onDismiss: () => void
  onRefresh?: () => void
  compact?: boolean
}) {
  const { data, loading, error: detailError, refetch } = useLivePositionDetail(position.id, [position.venue_a, position.venue_b])
  const pos = data?.position ?? position
  const fills = data?.fills ?? []
  const events = data?.events ?? []
  const [leg1Venue, leg2Venue] = monitoredLegVenues(fills, pos.venue_a, pos.venue_b)
  const liveClose = useLiveClose()
  const [confirmClose, setConfirmClose] = useState(false)

  const hasRecordedExposure = hasActionableRecordedFills(fills)
  const canClose = canRequestLiveClose(pos.state, fills)
  const isClosing = liveClose.state.phase !== 'idle' && liveClose.state.phase !== 'done' && liveClose.state.phase !== 'error'
  const closeDone = liveClose.state.phase === 'done'

  // Refresh the parent list once close tracking reaches a terminal UI state.
  useEffect(() => {
    if (closeDone || liveClose.state.phase === 'error') {
      onRefresh?.()
      refetch()
    }
  }, [closeDone, liveClose.state.phase, onRefresh, refetch])

  const handleClose = () => {
    setConfirmClose(false)
    liveClose.closePosition(pos.id, [pos.venue_a, pos.venue_b])
  }

  const reasonEvent = [...events].reverse().find(e =>
    e.event === 'complete' || e.event === 'close_leg_failed' || e.event === 'close_incomplete' ||
    e.event === 'session_recovery_blocked')
  const reason = reasonEvent?.detail

  return (
    <div className="flex min-h-0 flex-1 flex-col">
        {!compact && (
          <div className="flex items-center justify-between border-b border-border px-5 py-4">
            <div className="flex items-center gap-3">
              <AssetIcon asset={pos.asset} />
              <h2 className="text-lg font-semibold text-foreground">{pos.asset}</h2>
              <Badge variant="outline" className={`text-[11px] ${stateColor(pos.state)}`}>{pos.state}</Badge>
            </div>
            <button onClick={onDismiss} aria-label="Close position panel" className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
                <path d="M11 3L3 11M3 3l8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
              </svg>
            </button>
          </div>
        )}

        <div className="min-h-0 flex-1 overflow-y-auto">
        {/* Reason banner for non-open states */}
        {needsAttention(pos.state) && (
          <div className={`px-5 py-3 border-b ${
            pos.state === 'degraded' ? 'border-orange-500/20 bg-orange-500/[0.04]'
            : 'border-red-500/20 bg-red-500/[0.04]'
          }`}>
            <p className={`text-[11px] font-medium mb-1 ${pos.state === 'degraded' ? 'text-orange-400' : 'text-red-400'}`}>
              {pos.state === 'degraded'
                ? hasRecordedExposure ? 'Recorded exposure needs attention' : 'Exposure could not be reconstructed'
                : 'Execution failed'}
            </p>
            <p className="text-[10px] text-muted-foreground">
              {pos.state === 'degraded' && !hasRecordedExposure
                ? 'No actionable filled leg is recorded. Verify both venues directly before trading again.'
                : reason ?? 'Review the recorded fills and event timeline before taking another action.'}
            </p>
            <div className="mt-2 flex flex-wrap gap-2">
              <VenueTradeLink venue={pos.venue_a} symbol={pos.asset} />
              {pos.venue_b !== pos.venue_a && <VenueTradeLink venue={pos.venue_b} symbol={pos.asset} />}
            </div>
          </div>
        )}

        {/* Leg Fills */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">
            {pos.state === 'degraded' ? 'Recorded fills / possible exposure' : 'Leg Fills'}
          </p>
          {loading && <p className="text-[10px] text-muted-foreground">Loading...</p>}
          {!loading && fills.length === 0 && (
            <p className="text-[10px] text-muted-foreground">
              {pos.state === 'degraded'
                ? 'No fills recorded. Verify this account directly on both venues.'
                : 'No fills recorded.'}
            </p>
          )}
          {detailError && <p className="text-[10px] text-red-400">Could not load recorded fills: {detailError}</p>}
          {fills.length > 0 && (
            <div className="space-y-2">
              {fills.map((f) => (
                <FillCard key={f.id} fill={f} />
              ))}
            </div>
          )}
        </div>

        {/* Funding health — only for open positions with monitoring data */}
        {pos.state === 'open' && (pos.current_spread !== 0 || pos.basis_change !== 0) && (
          <div className="px-5 py-4 border-b border-border">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Funding</p>
            <div className="grid grid-cols-2 gap-4">
              <InfoItem label="Current Funding APR" value={fmtPct(pos.current_spread, 2)} warn={pos.current_spread < 0} />
              <InfoItem label="Basis Change" value={fmtPct(pos.basis_change)} warn={pos.basis_change < 0} />
            </div>
          </div>
        )}

        {/* Leg Monitoring — only for open positions with monitoring data */}
        {pos.state === 'open' && (pos.leg1_current_price > 0 || pos.leg2_current_price > 0) && (
          <div className="px-5 py-4 border-b border-border">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Leg Status</p>
            <div className={`grid gap-3 ${compact ? 'grid-cols-1' : 'grid-cols-2'}`}>
              <LegCard label="Leg 1" venue={leg1Venue} currentPrice={pos.leg1_current_price} liqPrice={pos.leg1_liq_price} liqDist={pos.leg1_liq_dist} liqRisk={pos.leg1_liq_risk} />
              <LegCard label="Leg 2" venue={leg2Venue} currentPrice={pos.leg2_current_price} liqPrice={pos.leg2_liq_price} liqDist={pos.leg2_liq_dist} liqRisk={pos.leg2_liq_risk} />
            </div>
          </div>
        )}

        {/* PnL — only for open positions */}
        {pos.state === 'open' && (pos.price_pnl !== 0 || pos.funding_pnl !== 0) && (
          <div className="px-5 py-4 border-b border-border">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Profit & Loss</p>
            <div className={`grid gap-4 ${compact ? 'grid-cols-2' : 'grid-cols-3'}`}>
              <div><p className="text-[10px] text-muted-foreground mb-0.5">Price PnL</p><p className={`text-sm font-mono font-medium ${pnlColor(pos.price_pnl)}`}>{formatSignedUsdPnL(pos.price_pnl)}</p></div>
              <div><p className="text-[10px] text-muted-foreground mb-0.5">{pos.funding_pnl_source === 'realized' ? 'Realized Funding' : 'Estimated Funding'}</p><p className={`text-sm font-mono font-medium ${pnlColor(pos.funding_pnl)}`}>{formatSignedUsdPnL(pos.funding_pnl)}</p></div>
              <div><p className="text-[10px] text-muted-foreground mb-0.5">Total PnL</p><p className={`text-sm font-mono font-semibold ${pnlColor(pos.total_pnl)}`}>{formatSignedUsdPnL(pos.total_pnl)}</p></div>
            </div>
          </div>
        )}

        {/* Position Info */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Position Info</p>
          <div className="grid grid-cols-2 gap-x-6 gap-y-3">
            <InfoItem label="Leverage" value={`${pos.leverage}x`} />
            {pos.hold_hours > 0 && <InfoItem label="Hold Time" value={fmtHours(pos.hold_hours)} />}
          </div>
        </div>

        {/* Timestamps */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Timeline</p>
          <div className={`grid gap-y-3 gap-x-6 ${compact ? 'grid-cols-1' : 'grid-cols-2'}`}>
            <InfoItem label="Started" value={fmtTime(pos.started_at)} />
            <InfoItem label="Opened" value={fmtTime(pos.opened_at)} />
            <InfoItem label="Last Updated" value={fmtTime(pos.updated_at)} />
            {pos.completed_at && <InfoItem label="Completed" value={fmtTime(pos.completed_at)} />}
          </div>
        </div>

        {/* Operational log follows the higher-level position timeline. */}
        {events.length > 0 && (
          <div className="px-5 py-4">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Event Timeline</p>
            <div className="flex flex-col gap-3">
              {events.map((ev) => (
                <EventRow key={ev.id} event={ev} />
              ))}
            </div>
          </div>
        )}
        </div>

        {(canClose || isClosing || closeDone) && (
          <div
            data-slot="position-close-cta"
            className="shrink-0 border-t border-border bg-card px-5 py-4"
          >
            {canClose && !confirmClose && !isClosing && !closeDone && (
              <Button variant="destructive" size="lg" className="w-full font-medium" onClick={() => setConfirmClose(true)}>
                {pos.state === 'open' ? 'Close Position' : 'Check & Close Venue Exposure'}
              </Button>
            )}
            {confirmClose && !isClosing && (
              <div className="flex flex-col gap-3">
                <p className="text-[11px] leading-relaxed text-muted-foreground">
                  {pos.state !== 'open'
                    ? 'Refresh both venues and close any position for this asset? This may include exposure opened outside Orbital.'
                    : 'Close both legs? Local authorization keys will sign each reduce-only order.'}
                </p>
                <div className="grid grid-cols-2 gap-2">
                  <Button variant="secondary" size="lg" onClick={() => setConfirmClose(false)}>Cancel</Button>
                  <Button variant="destructive" size="lg" onClick={handleClose}>Confirm</Button>
                </div>
              </div>
            )}
            {isClosing && (
              <p className="text-[11px] leading-relaxed text-yellow-400">
                {liveClose.state.phase === 'preparing' ? (pos.state !== 'open' ? 'Checking venue state...' : 'Preparing close orders...') :
                  liveClose.state.phase === 'signing' ? `Signing close order ${liveClose.state.submitted + 1} of ${liveClose.state.total} with local authorization` :
                  liveClose.state.phase === 'confirming' ? 'Waiting for confirmed close fills...' :
                  `Submitting ${liveClose.state.submitted + 1} of ${liveClose.state.total}...`}
              </p>
            )}
            {closeDone && liveClose.state.failed === 0 && (
              <p className="text-[11px] leading-relaxed text-green-400">
                {liveClose.state.reconciled
                  ? 'Venue state verified; no remaining exposure was found.'
                  : 'Position closed with all leg fills confirmed.'}
              </p>
            )}
            {closeDone && liveClose.state.failed > 0 && (
              <div className="flex flex-col gap-1 text-[11px]">
                <p className="text-yellow-400">{liveClose.state.succeeded} accepted, {liveClose.state.failed} failed</p>
                {liveClose.state.outcomes.filter((outcome) => outcome.status === 'failed').map((outcome, index) => (
                  <CloseFailure key={`${outcome.venue}-${outcome.symbol}-${index}`} outcome={outcome} />
                ))}
              </div>
            )}
            {liveClose.state.phase === 'error' && (
              <div className="flex flex-col gap-2">
                <p className="break-words text-[11px] leading-relaxed text-red-400">{liveClose.state.errors[0]}</p>
                <div className="flex flex-wrap gap-2">
                  <VenueTradeLink venue={pos.venue_a} symbol={pos.asset} />
                  {pos.venue_b !== pos.venue_a && <VenueTradeLink venue={pos.venue_b} symbol={pos.asset} />}
                </div>
              </div>
            )}
          </div>
        )}
    </div>
  )
}

function VenueTradeLink({ venue, symbol }: { venue: string; symbol: string }) {
  const href = venueTradeUrl(venue, symbol)
  if (!href) return null
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1 rounded border border-border bg-white/[0.03] px-2 py-1 text-[10px] capitalize text-muted-foreground transition-colors hover:bg-white/[0.07] hover:text-foreground"
    >
      Open {venue}<ExternalLinkIcon className="size-3" />
    </a>
  )
}

function CloseFailure({ outcome }: { outcome: CloseOutcome }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded border border-red-500/15 bg-red-500/[0.03] px-2 py-1.5">
      <p className="text-red-400/70">{outcome.venue} {outcome.symbol}: {outcome.error ?? 'rejected'}</p>
      <VenueTradeLink venue={outcome.venue} symbol={outcome.symbol} />
    </div>
  )
}

function FillCard({ fill }: { fill: LiveFillDetail }) {
  const metadata = venueMetadata(fill.venue)
  const isGood = fill.filled
  const isBad = !fill.accepted || (fill.error && fill.error.length > 0)
  const hasFillMismatch = fill.requested_amount > 0 && Math.abs(fill.filled_amount - fill.requested_amount) > fill.requested_amount * 0.0001

  return (
    <div className={`rounded-lg border px-3 py-2.5 ${
      isGood ? 'border-green-500/15 bg-green-500/[0.02]'
      : isBad ? 'border-red-500/15 bg-red-500/[0.02]'
      : 'border-border bg-white/[0.02]'
    }`}>
      <div className="flex items-center gap-2 mb-1.5">
        <span className="text-[10px] text-muted-foreground font-medium">Leg {fill.leg}</span>
        {metadata.logo && <img src={metadata.logo} alt={metadata.label} className="size-3.5 rounded-sm" />}
        <span className="text-[11px] text-foreground">{metadata.label}</span>
        <span className={`ml-auto text-[10px] font-medium ${
          fill.filled ? 'text-green-400' : fill.accepted ? 'text-yellow-400' : 'text-red-400'
        }`}>
          {fill.filled ? 'Filled' : fill.accepted ? 'Accepted' : 'Rejected'}
        </span>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-[10px]">
        <span><span className="text-muted-foreground">Side: </span><span className={`font-medium ${fill.side === 'long' ? 'text-green-400' : 'text-red-400'}`}>{fill.side}</span></span>
        <span><span className="text-muted-foreground">Size: </span><span className="font-mono text-foreground">{fill.filled_amount > 0 ? fill.filled_amount.toPrecision(4) : '—'}</span></span>
        {hasFillMismatch && <span><span className="text-muted-foreground">Requested: </span><span className="font-mono text-foreground">{fill.requested_amount.toPrecision(4)}</span></span>}
        <span><span className="text-muted-foreground">Avg Price: </span><span className="font-mono text-foreground">{fill.avg_fill_price > 0 ? fmtPrice(fill.avg_fill_price) : '—'}</span></span>
        {hasFillMismatch && <span><span className="text-muted-foreground">Filled: </span><span className="font-mono text-foreground">{fmtPct(fill.fill_ratio, 1)}</span></span>}
        <span><span className="text-muted-foreground">Fee: </span><span className="font-mono text-foreground">{fill.fee > 0 ? `$${fill.fee.toFixed(4)}` : '—'}</span></span>
      </div>
      {fill.error && (
        <p className="text-[10px] text-red-400/70 mt-1">{fill.error}</p>
      )}
    </div>
  )
}

function EventRow({ event: ev }: { event: LiveEventDetail }) {
  const isComplete = ev.event === 'complete'
  const isError = ev.state === 'degraded' || ev.state === 'failed'
  const label = ev.event
    .split('_')
    .filter(Boolean)
    .map((word, index) => index === 0 ? word.charAt(0).toUpperCase() + word.slice(1) : word)
    .join(' ')

  return (
    <div className="min-w-0 text-[10px]">
      <div className="flex min-w-0 items-baseline justify-between gap-3">
        <span className={`min-w-0 font-medium leading-relaxed ${
          isComplete && isError ? 'text-red-400' : isComplete ? 'text-green-400' : 'text-foreground'
        }`}>{label}</span>
        <span className="shrink-0 whitespace-nowrap font-mono text-[9px] text-muted-foreground/60">{fmtEventTime(ev.at)}</span>
      </div>
      {ev.detail && <p className="mt-1 min-w-0 break-words leading-relaxed text-muted-foreground [overflow-wrap:anywhere]">{ev.detail}</p>}
    </div>
  )
}

function LegCard({ label, venue, currentPrice, liqPrice, liqDist, liqRisk }: {
  label: string; venue: string; currentPrice: number; liqPrice: number; liqDist: number; liqRisk: string
}) {
  const metadata = venueMetadata(venue)
  return (
    <div className="rounded-lg border border-border bg-white/[0.02] px-3 py-3">
      <div className="flex items-center justify-between gap-3 mb-2.5">
        <span className="text-[10px] text-muted-foreground font-medium">{label}</span>
        <div className="flex min-w-0 items-center gap-2">
          {metadata.logo && <img src={metadata.logo} alt="" className="size-4 rounded-sm" />}
          <span className="truncate text-xs text-foreground">{metadata.label}</span>
        </div>
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
    <div className="min-w-0">
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <p className={`break-words text-sm font-mono [overflow-wrap:anywhere] ${warn ? 'text-orange-400' : 'text-foreground'}`}>{value}</p>
    </div>
  )
}
