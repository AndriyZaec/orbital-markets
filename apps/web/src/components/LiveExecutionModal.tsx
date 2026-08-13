import { useEffect, useState } from 'react'
import type { LiveExecutionState, ExecutionPhase, LegFillView, UnwindStatus } from '@/hooks/useLiveExecution'
import { recoveryPresentation, type RecoveryTone } from '@/lib/degraded-execution'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'

interface Props {
  state: LiveExecutionState
  onRetry: () => void
  onClose: () => void
  onViewPositions: () => void
}

const TERMINAL: ExecutionPhase[] = ['open', 'degraded', 'aborted', 'failed', 'recovering']

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

type LegStatus = 'pending' | 'signing' | 'submitting' | 'checking' | 'accepted' | 'partial' | 'failed' | 'unwound' | 'skipped'

function leg1Status(phase: ExecutionPhase, unwindStatus: UnwindStatus): LegStatus {
  switch (phase) {
    case 'preparing': return 'pending'
    case 'awaiting_leg1': return 'signing'
    case 'submitting_leg1': return 'submitting'
    case 'awaiting_leg2':
    case 'submitting_leg2':
    case 'awaiting_leg2_retry':
    case 'submitting_leg2_retry':
    case 'open': return 'accepted'
    case 'recovering': return 'checking'
    case 'degraded':
    case 'aborted':
      if (unwindStatus === 'confirmed') return 'unwound'
      if (unwindStatus === 'unconfirmed' || unwindStatus === 'submit_failed') return 'failed'
      return 'failed'
    case 'failed': return 'failed'
    default: return 'pending'
  }
}

function leg2Status(phase: ExecutionPhase, fill: LegFillView | null): LegStatus {
  switch (phase) {
    case 'awaiting_leg2': return 'signing'
    case 'submitting_leg2': return 'submitting'
    case 'awaiting_leg2_retry': return 'signing'
    case 'submitting_leg2_retry': return 'submitting'
    case 'open': return 'accepted'
    case 'recovering': return 'checking'
    case 'degraded': return fill && fill.filled_amount > 0 ? 'partial' : 'failed'
    case 'aborted': return 'skipped'
    default: return 'pending'
  }
}

const STATUS_STYLE: Record<LegStatus, { dot: string; text: string; label: string }> = {
  pending: { dot: 'bg-zinc-500', text: 'text-muted-foreground', label: 'Pending' },
  signing: { dot: 'bg-yellow-400 animate-pulse', text: 'text-yellow-400', label: 'Awaiting Signature...' },
  submitting: { dot: 'bg-blue-400 animate-pulse', text: 'text-blue-400', label: 'Submitting...' },
  checking: { dot: 'bg-blue-400 animate-pulse', text: 'text-blue-400', label: 'Checking venue state' },
  accepted: { dot: 'bg-green-400', text: 'text-green-400', label: 'Filled' },
  partial: { dot: 'bg-orange-400', text: 'text-orange-400', label: 'Partial fill' },
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
      : status === 'partial' ? 'border-orange-500/20 bg-orange-500/[0.03]'
      : status === 'checking' ? 'border-blue-500/20 bg-blue-500/[0.03]'
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

function UnwindNotice({ status }: { status: UnwindStatus }) {
  if (!status) return null
  switch (status) {
    case 'confirmed':
      return <p className="text-[10px] text-muted-foreground mt-1">Leg 1 was unwound via the pre-signed order.</p>
    case 'unconfirmed':
      return <p className="text-[10px] text-red-400/80 mt-1">Leg 1 unwind was submitted but fill was not confirmed. Manual close or kill switch may be required.</p>
    case 'submit_failed':
      return <p className="text-[10px] text-red-400 mt-1">Leg 1 unwind failed to submit. Manual close or kill switch is required.</p>
    case 'not_armed':
      return <p className="text-[10px] text-muted-foreground mt-1">No unwind was armed for this session.</p>
    case 'skipped':
      return <p className="text-[10px] text-orange-300/80 mt-1">Leg 1 unwind was skipped because it would increase directional exposure.</p>
    default:
      return null
  }
}

const PHASE_HINT: Partial<Record<ExecutionPhase, string>> = {
  preparing: 'Preparing execution plan...',
  awaiting_leg1: 'Local authorization is signing the riskier leg and its safety unwind',
  submitting_leg1: 'Submitting riskier leg and waiting for fill...',
  awaiting_leg2: 'Sign the hedge leg (sized from the actual fill)',
  submitting_leg2: 'Submitting hedge leg and verifying hedge...',
  awaiting_leg2_retry: 'Sign one residual hedge retry',
  submitting_leg2_retry: 'Submitting the residual hedge retry...',
  recovering: 'Reconciling venue positions after an uncertain result...',
}

const TONE_STYLE: Record<RecoveryTone, { border: string; background: string; title: string; button: string }> = {
  green: { border: 'border-green-500/20', background: 'bg-green-500/[0.04]', title: 'text-green-400', button: 'bg-green-600 hover:bg-green-500' },
  blue: { border: 'border-blue-500/20', background: 'bg-blue-500/[0.04]', title: 'text-blue-400', button: 'bg-blue-600 hover:bg-blue-500' },
  orange: { border: 'border-orange-500/20', background: 'bg-orange-500/[0.04]', title: 'text-orange-400', button: 'bg-orange-600 hover:bg-orange-500' },
  red: { border: 'border-red-500/20', background: 'bg-red-500/[0.04]', title: 'text-red-400', button: 'bg-red-600 hover:bg-red-500' },
}

export function LiveExecutionModal({ state, onRetry, onClose, onViewPositions }: Props) {
  const countdown = useCountdownToExpiry(state.expiresAt)
  const isTerminal = TERMINAL.includes(state.phase)
  const leg1Venue = state.riskierVenue
  const leg2Venue = state.hedgeVenue
  const leg1Amount = state.leg1Requests[0]?.amount
  const leg2Amount = state.leg2Request?.amount
  const terminalPresentation = isTerminal
    ? recoveryPresentation(state.phase as 'open' | 'recovering' | 'degraded' | 'aborted' | 'failed', state.unwindStatus, state.remainingExposure.length)
    : null
  const terminalStyle = terminalPresentation ? TONE_STYLE[terminalPresentation.tone] : null

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
              <LegCard label="Leg 1 · Riskier" venue={leg1Venue} status={leg1Status(state.phase, state.unwindStatus)} amount={leg1Amount} fill={state.leg1Fill} />
              <LegCard label="Leg 2 · Hedge" venue={leg2Venue} status={leg2Status(state.phase, state.leg2Fill)} amount={leg2Amount} fill={state.leg2Fill} />
            </div>
          )}

          {/* Armed-unwind reassurance during the exposed window */}
          {(state.phase === 'awaiting_leg2' || state.phase === 'submitting_leg2' ||
            state.phase === 'awaiting_leg2_retry' || state.phase === 'submitting_leg2_retry') && (
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
          {terminalPresentation && terminalStyle && (
            <div className={`rounded border px-4 py-3 ${terminalStyle.border} ${terminalStyle.background}`}>
              <p className={`text-xs font-medium ${terminalStyle.title}`}>{terminalPresentation.title}</p>
              <p className="mt-1 text-[10px] text-muted-foreground">{terminalPresentation.description}</p>
              {(state.error || state.reason) && (
                <p className={`mt-2 text-[11px] ${terminalStyle.title} opacity-75`}>{state.error || state.reason}</p>
              )}
              {(state.phase === 'degraded' || state.phase === 'aborted') && <UnwindNotice status={state.unwindStatus} />}
              {state.remainingExposure.length > 0 && (
                <div className="mt-3 space-y-1.5 rounded border border-orange-500/15 bg-black/10 px-3 py-2">
                  <p className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Last reported exposure</p>
                  {state.remainingExposure.map((exposure) => {
                    const fill = exposure.leg === 1 ? state.leg1Fill : state.leg2Fill
                    const usd = fill && fill.avg_price > 0 ? exposure.amount * fill.avg_price : null
                    return (
                      <p key={`${exposure.leg}-${exposure.venue}`} className="text-[10px] font-mono text-orange-300">
                        {exposure.venue} · {exposure.side} {fmtAmount(exposure.amount)} {exposure.symbol}
                        {usd !== null ? ` · ≈$${usd.toFixed(2)}` : ''}
                      </p>
                    )
                  })}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Footer actions */}
        {isTerminal && (
          <div className="px-5 py-4 border-t border-border flex gap-2">
            {terminalPresentation && terminalStyle && (
              <button
                onClick={terminalPresentation.action === 'retry' ? onRetry : onViewPositions}
                className={`flex-1 py-2 rounded-lg text-xs font-medium text-white transition-colors ${terminalStyle.button}`}
              >
                {terminalPresentation.actionLabel}
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
