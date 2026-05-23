import { useEffect, useState } from 'react'
import type { LiveExecutionState, ExecutionPhase } from '@/hooks/useLiveExecution'
import type { SigningRequest } from '@/types/signing'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'

interface Props {
  state: LiveExecutionState
  onRetry: () => void
  onClose: () => void
  onViewPositions: () => void
}

function fmtUsd(n: number) {
  if (n >= 1_000_000) return '$' + (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return '$' + (n / 1_000).toFixed(1) + 'K'
  return '$' + n.toFixed(2)
}

function fmtPrice(n: number) {
  if (n >= 1000) return '$' + n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  if (n >= 1) return '$' + n.toFixed(4)
  return '$' + n.toPrecision(4)
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

function legStatus(phase: ExecutionPhase, legIndex: 0 | 1): 'pending' | 'signing' | 'submitting' | 'accepted' | 'failed' {
  if (phase === 'failed') return 'failed'
  if (phase === 'confirmed') return 'accepted'

  if (legIndex === 0) {
    if (phase === 'preparing') return 'pending'
    if (phase === 'awaiting_signature_1') return 'signing'
    if (phase === 'submitting_1') return 'submitting'
    return 'accepted'
  }

  // leg 1
  if (phase === 'preparing' || phase === 'awaiting_signature_1' || phase === 'submitting_1') return 'pending'
  if (phase === 'awaiting_signature_2') return 'signing'
  if (phase === 'submitting_2') return 'submitting'
  return 'accepted'
}

const STATUS_STYLE: Record<string, { dot: string; text: string; label: string }> = {
  pending: { dot: 'bg-zinc-500', text: 'text-muted-foreground', label: 'Pending' },
  signing: { dot: 'bg-yellow-400 animate-pulse', text: 'text-yellow-400', label: 'Awaiting Signature...' },
  submitting: { dot: 'bg-blue-400 animate-pulse', text: 'text-blue-400', label: 'Submitting...' },
  accepted: { dot: 'bg-green-400', text: 'text-green-400', label: 'Accepted' },
  failed: { dot: 'bg-red-400', text: 'text-red-400', label: 'Failed' },
}

const venueLogos: Record<string, string> = { pacifica: pacificaLogo, hyperliquid: hlLogo }

function LegCard({ request, status, orderId }: { request: SigningRequest; status: string; orderId?: string }) {
  const s = STATUS_STYLE[status] ?? STATUS_STYLE.pending
  const logo = venueLogos[request.venue]

  return (
    <div className={`rounded-lg border px-4 py-3 ${
      status === 'accepted' ? 'border-green-500/20 bg-green-500/[0.03]'
      : status === 'failed' ? 'border-red-500/20 bg-red-500/[0.03]'
      : status === 'signing' || status === 'submitting' ? 'border-yellow-500/20 bg-yellow-500/[0.03]'
      : 'border-border bg-white/[0.02]'
    }`}>
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2">
          {logo && <img src={logo} alt={request.venue} className="size-5 rounded-sm" />}
          <span className="text-xs font-semibold text-foreground capitalize">{request.venue}</span>
        </div>
        <div className="flex items-center gap-1.5">
          <div className={`size-1.5 rounded-full ${s.dot}`} />
          <span className={`text-[10px] font-medium ${s.text}`}>{s.label}</span>
        </div>
      </div>
      <div className="flex items-center gap-4 text-[11px]">
        <div>
          <span className="text-muted-foreground">Side: </span>
          <span className={`font-medium ${request.side === 'buy' ? 'text-green-400' : 'text-red-400'}`}>
            {request.side.toUpperCase()}
          </span>
        </div>
        <div>
          <span className="text-muted-foreground">Size: </span>
          <span className="text-foreground font-mono">{fmtUsd(request.amount)}</span>
        </div>
        <div>
          <span className="text-muted-foreground">Price: </span>
          <span className="text-foreground font-mono">{fmtPrice(request.price)}</span>
        </div>
      </div>
      {orderId && (
        <p className="text-[10px] text-muted-foreground/60 font-mono mt-1.5 truncate">Order: {orderId}</p>
      )}
    </div>
  )
}

export function LiveExecutionModal({ state, onRetry, onClose, onViewPositions }: Props) {
  const countdown = useCountdownToExpiry(state.expiresAt)
  const isTerminal = state.phase === 'confirmed' || state.phase === 'failed'

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
            {state.phase === 'preparing' && (
              <p className="text-[10px] text-muted-foreground mt-0.5">Preparing execution plan...</p>
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
          {/* Progress steps */}
          {state.phase !== 'idle' && (
            <div className="flex flex-col gap-3 mb-4">
              {state.signingRequests.map((req, i) => (
                <LegCard
                  key={req.id}
                  request={req}
                  status={legStatus(state.phase, i as 0 | 1)}
                  orderId={state.results[i]?.order_id}
                />
              ))}
            </div>
          )}

          {/* Preparing state */}
          {state.phase === 'preparing' && (
            <div className="flex items-center justify-center py-8">
              <div className="relative size-8 animate-[loader-pulse_2s_ease-in-out_infinite]">
                <div className="absolute inset-0 rounded-full border-2 border-slate-500/40" />
                <div className="absolute inset-1.5 rounded-full border-[1.5px] border-slate-400/50" />
                <div className="absolute inset-0 flex items-center justify-center">
                  <div className="size-2 rounded-full bg-cyan-400 shadow-[0_0_8px_rgba(6,182,212,0.6)]" />
                </div>
              </div>
            </div>
          )}

          {/* Signing prompt */}
          {(state.phase === 'awaiting_signature_1' || state.phase === 'awaiting_signature_2') && (
            <div className="rounded border border-yellow-500/20 bg-yellow-500/[0.04] px-4 py-3 text-center">
              <p className="text-xs text-yellow-400">
                Approve signing in your {state.currentVenue === 'pacifica' ? 'Solana' : 'EVM'} wallet...
              </p>
            </div>
          )}

          {/* Error state */}
          {state.phase === 'failed' && state.error && (
            <div className="rounded border border-red-500/20 bg-red-500/[0.04] px-4 py-3">
              <p className="text-xs text-red-400 font-medium mb-1">Execution Failed</p>
              <p className="text-[11px] text-red-400/70">{state.error}</p>
              {state.failedVenue && (
                <p className="text-[10px] text-muted-foreground mt-1">Failed venue: {state.failedVenue}</p>
              )}
            </div>
          )}

          {/* Success state */}
          {state.phase === 'confirmed' && (
            <div className="rounded border border-green-500/20 bg-green-500/[0.04] px-4 py-3 text-center">
              <p className="text-xs text-green-400 font-medium">Both legs accepted</p>
              <p className="text-[10px] text-muted-foreground mt-1">Position opened successfully.</p>
            </div>
          )}
        </div>

        {/* Footer actions */}
        {isTerminal && (
          <div className="px-5 py-4 border-t border-border flex gap-2">
            {state.phase === 'failed' && (
              <button
                onClick={onRetry}
                className="flex-1 py-2 rounded-lg text-xs font-medium bg-blue-600 text-white hover:bg-blue-500 transition-colors"
              >
                Try Again
              </button>
            )}
            {state.phase === 'confirmed' && (
              <button
                onClick={onViewPositions}
                className="flex-1 py-2 rounded-lg text-xs font-medium bg-green-600 text-white hover:bg-green-500 transition-colors"
              >
                View Positions
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
