import { useEffect, useState } from 'react'
import type { LiveExecutionState, ExecutionPhase, LegFillView } from '@/hooks/useLiveExecution'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'

interface Props {
  state: LiveExecutionState
  onRetry: () => void
  onClose: () => void
  onViewPositions: () => void
}

const TERMINAL: ExecutionPhase[] = ['open', 'degraded', 'aborted', 'failed']

function fmtAmount(n: number) {
  if (n >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 })
  return n.toPrecision(4)
}

function useCountdownToExpiry(expiresAt: string | null) {
  const [remaining, setRemaining] = useState(0)
  useEffect(() => {
    if (!expiresAt) return
    const update = () => {
      const ms = new Date(expiresAt).getTime() - Date.now()
      setRemaining(Math.max(0, Math.ceil(ms / 1000)))
    }
    update()
    const id = setInterval(update, 1000)
    return () => clearInterval(id)
  }, [expiresAt])
  return remaining
}

type LegStatus = 'pending' | 'signing' | 'submitting' | 'accepted' | 'failed' | 'unwound' | 'skipped'

function leg1Status(phase: ExecutionPhase): LegStatus {
  switch (phase) {
    case 'preparing': return 'pending'
    case 'awaiting_leg1': return 'signing'
    case 'submitting_leg1': return 'submitting'
    case 'awaiting_leg2':
    case 'submitting_leg2':
    case 'open': return 'accepted'
    case 'degraded':
    case 'aborted': return 'unwound'
    case 'failed': return 'failed'
    default: return 'pending'
  }
}

function leg2Status(phase: ExecutionPhase): LegStatus {
  switch (phase) {
    case 'awaiting_leg2': return 'signing'
    case 'submitting_leg2': return 'submitting'
    case 'open': return 'accepted'
    case 'degraded': return 'failed'
    case 'aborted': return 'skipped'
    default: return 'pending'
  }
}

const STATUS_STYLE: Record<LegStatus, { dot: string; text: string; label: string }> = {
  pending: { dot: 'bg-zinc-500', text: 'text-muted-foreground', label: 'Pending' },
  signing: { dot: 'bg-yellow-400 animate-pulse', text: 'text-yellow-400', label: 'Awaiting Signature...' },
  submitting: { dot: 'bg-blue-400 animate-pulse', text: 'text-blue-400', label: 'Submitting...' },
  accepted: { dot: 'bg-green-400', text: 'text-green-400', label: 'Filled' },
  failed: { dot: 'bg-red-400', text: 'text-red-400', label: 'Failed' },
  unwound: { dot: 'bg-orange-400', text: 'text-orange-400', label: 'Unwound' },
  skipped: { dot: 'bg-zinc-600', text: 'text-muted-foreground', label: 'Not attempted' },
}

const venueLogos: Record<string, string> = { pacifica: pacificaLogo, hyperliquid: hlLogo }

function LegCard({
  label, venue, status, amount, fill,
}: { label: string; venue: string | null; status: LegStatus; amount?: number; fill?: LegFillView | null }) {
  const s = STATUS_STYLE[status]
  const logo = venue ? venueLogos[venue] : undefined
  return (
    <div className={`rounded-lg border px-4 py-3 ${
      status === 'accepted' ? 'border-green-500/20 bg-green-500/[0.03]'
      : status === 'failed' ? 'border-red-500/20 bg-red-500/[0.03]'
      : status === 'unwound' ? 'border-orange-500/20 bg-orange-500/[0.03]'
      : status === 'signing' || status === 'submitting' ? 'border-yellow-500/20 bg-yellow-500/[0.03]'
      : 'border-border bg-white/[0.02]'
    }`}>
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2">
          {logo && <img src={logo} alt={venue ?? ''} className="size-5 rounded-sm" />}
          <span className="text-xs font-semibold text-foreground">{label}</span>
          {venue && <span className="text-[10px] text-muted-foreground capitalize">{venue}</span>}
        </div>
        <div className="flex items-center gap-1.5">
          <div className={`size-1.5 rounded-full ${s.dot}`} />
          <span className={`text-[10px] font-medium ${s.text}`}>{s.label}</span>
        </div>
      </div>
      <div className="flex items-center gap-4 text-[11px]">
        {amount != null && amount > 0 && (
          <div>
            <span className="text-muted-foreground">Size: </span>
            <span className="text-foreground font-mono">{fmtAmount(amount)}</span>
          </div>
        )}
        {fill && fill.filled_amount > 0 && (
          <>
            <div>
              <span className="text-muted-foreground">Filled: </span>
              <span className="text-foreground font-mono">{fmtAmount(fill.filled_amount)}</span>
            </div>
            <div>
              <span className="text-muted-foreground">@ </span>
              <span className="text-foreground font-mono">{fill.avg_price.toFixed(4)}</span>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

const PHASE_HINT: Partial<Record<ExecutionPhase, string>> = {
  preparing: 'Preparing execution plan...',
  awaiting_leg1: 'Sign the riskier leg + its safety unwind in your wallet',
  submitting_leg1: 'Submitting riskier leg and waiting for fill...',
  awaiting_leg2: 'Sign the hedge leg (sized from the actual fill)',
  submitting_leg2: 'Submitting hedge leg and verifying hedge...',
}

export function LiveExecutionModal({ state, onRetry, onClose, onViewPositions }: Props) {
  const countdown = useCountdownToExpiry(state.expiresAt)
  const isTerminal = TERMINAL.includes(state.phase)
  const leg1Venue = state.riskierVenue
  const leg2Venue = state.hedgeVenue
  const leg1Amount = state.leg1Requests[0]?.amount
  const leg2Amount = state.leg2Request?.amount

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={isTerminal ? onClose : undefined} />
      <div className="relative w-[440px] max-h-[80vh] bg-card border border-border rounded-xl shadow-2xl flex flex-col overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <div>
            <h2 className="text-sm font-semibold text-foreground">
              Live Execution{state.asset ? ` — ${state.asset}` : ''}
            </h2>
            {PHASE_HINT[state.phase] && (
              <p className="text-[10px] text-muted-foreground mt-0.5">{PHASE_HINT[state.phase]}</p>
            )}
          </div>
          <div className="flex items-center gap-3">
            {state.expiresAt && !isTerminal && (
              <div className={`flex items-center gap-1.5 px-2 py-0.5 rounded border ${
                countdown <= 10 ? 'border-red-500/30 text-red-400' : 'border-border text-muted-foreground'
              }`}>
                <svg width="10" height="10" viewBox="0 0 16 16" fill="none" className="shrink-0">
                  <circle cx="8" cy="8" r="6.5" stroke="currentColor" strokeWidth="1.2"/>
                  <path d="M8 4.5V8l2.5 1.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
                </svg>
                <span className="text-[11px] font-mono font-medium">{countdown}s</span>
              </div>
            )}
            {isTerminal && (
              <button onClick={onClose} className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors">
                <svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/></svg>
              </button>
            )}
          </div>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-auto px-5 py-4">
          {state.phase !== 'idle' && (
            <div className="flex flex-col gap-3 mb-4">
              <LegCard label="Leg 1 · Riskier" venue={leg1Venue} status={leg1Status(state.phase)} amount={leg1Amount} fill={state.leg1Fill} />
              <LegCard label="Leg 2 · Hedge" venue={leg2Venue} status={leg2Status(state.phase)} amount={leg2Amount} fill={state.leg2Fill} />
            </div>
          )}

          {/* Armed-unwind reassurance during the exposed window */}
          {(state.phase === 'awaiting_leg2' || state.phase === 'submitting_leg2') && (
            <p className="text-[10px] text-muted-foreground/70 text-center mb-3">
              Safety: leg-1 unwind is pre-signed — if the hedge fails, it closes automatically.
            </p>
          )}

          {/* Mismatch readout */}
          {state.mismatch != null && (
            <p className="text-[11px] text-muted-foreground text-center mb-3">
              Hedge mismatch: <span className="font-mono text-foreground">{(state.mismatch * 100).toFixed(2)}%</span>
            </p>
          )}

          {/* Terminal banners */}
          {state.phase === 'open' && (
            <div className="rounded border border-green-500/20 bg-green-500/[0.04] px-4 py-3 text-center">
              <p className="text-xs text-green-400 font-medium">Hedge opened</p>
              <p className="text-[10px] text-muted-foreground mt-1">Both legs filled within the hedge tolerance.</p>
            </div>
          )}
          {(state.phase === 'degraded' || state.phase === 'aborted') && (
            <div className="rounded border border-orange-500/20 bg-orange-500/[0.04] px-4 py-3">
              <p className="text-xs text-orange-400 font-medium mb-1">
                {state.phase === 'degraded' ? 'Hedge not established' : 'Open aborted'}
              </p>
              {state.reason && <p className="text-[11px] text-orange-400/70">{state.reason}</p>}
              {state.unwound && (
                <p className="text-[10px] text-muted-foreground mt-1">Leg 1 was unwound via the pre-signed order.</p>
              )}
            </div>
          )}
          {state.phase === 'failed' && (
            <div className="rounded border border-red-500/20 bg-red-500/[0.04] px-4 py-3">
              <p className="text-xs text-red-400 font-medium mb-1">Execution failed</p>
              {(state.error || state.reason) && (
                <p className="text-[11px] text-red-400/70">{state.error || state.reason}</p>
              )}
            </div>
          )}
        </div>

        {/* Footer actions */}
        {isTerminal && (
          <div className="px-5 py-4 border-t border-border flex gap-2">
            {state.phase === 'open' ? (
              <button
                onClick={onViewPositions}
                className="flex-1 py-2 rounded-lg text-xs font-medium bg-green-600 text-white hover:bg-green-500 transition-colors"
              >
                View Positions
              </button>
            ) : (
              <button
                onClick={onRetry}
                className="flex-1 py-2 rounded-lg text-xs font-medium bg-blue-600 text-white hover:bg-blue-500 transition-colors"
              >
                Try Again
              </button>
            )}
            <button
              onClick={onClose}
              className="flex-1 py-2 rounded-lg text-xs font-medium bg-white/[0.06] text-muted-foreground hover:text-foreground hover:bg-white/[0.1] transition-colors"
            >
              Close
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
