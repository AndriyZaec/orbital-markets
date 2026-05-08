import { useEffect, useState } from 'react'
import type { ExecutionPlan, Leg } from '@/hooks/usePlan'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

interface Props {
  plan: ExecutionPlan
  loading: boolean
  error: string | null
  leverage: number
  onLeverageChange: (lev: number) => void
  onClose: () => void
  onExecute: (opportunityId: string) => void
}

function fmtPrice(n: number) {
  if (n >= 1000) return n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  if (n >= 1) return n.toFixed(4)
  return n.toPrecision(4)
}

function fmtPct(n: number, decimals = 2) {
  return (n * 100).toFixed(decimals) + '%'
}

function fmtUsd(n: number) {
  if (n >= 1_000_000) return '$' + (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return '$' + (n / 1_000).toFixed(1) + 'K'
  return '$' + n.toFixed(2)
}

function useExpiry(expiresAt: string) {
  const [remaining, setRemaining] = useState(0)
  const [expired, setExpired] = useState(false)

  useEffect(() => {
    const update = () => {
      const ms = new Date(expiresAt).getTime() - Date.now()
      if (ms <= 0) {
        setRemaining(0)
        setExpired(true)
      } else {
        setRemaining(Math.ceil(ms / 1000))
        setExpired(false)
      }
    }
    update()
    const id = setInterval(update, 1000)
    return () => clearInterval(id)
  }, [expiresAt])

  return { remaining, expired }
}

const LEVERAGE_OPTIONS = [1, 2, 3, 5]

export function PlanPreview({ plan, loading, error, leverage, onLeverageChange, onClose, onExecute }: Props) {
  const { remaining, expired } = useExpiry(plan.expires_at)

  return (
    <div className="fixed inset-0 bg-black/70 flex items-center justify-center z-50" onClick={onClose}>
      <div
        className="bg-card border border-border rounded-lg w-[520px] max-h-[90vh] overflow-y-auto shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-5 pt-5 pb-4 border-b border-border">
          <div className="flex items-center justify-between mb-2">
            <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">Execution Plan</p>
            <button
              onClick={onClose}
              className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors"
            >
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
                <path d="M11 3L3 11M3 3l8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
              </svg>
            </button>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xl font-bold text-foreground">{plan.asset}</span>
            <Badge variant={plan.confidence === 'high' ? 'default' : 'secondary'} className="text-[11px]">{plan.confidence}</Badge>
            <FreshnessIndicator remaining={remaining} expired={expired} loading={loading} />
          </div>
        </div>

        {/* Error */}
        {error && (
          <div className="px-5 py-2.5 bg-red-500/10 text-red-400 text-sm border-b border-border">
            Plan refresh failed: {error}
          </div>
        )}

        {/* Legs */}
        <div className="px-5 py-4 border-b border-border flex flex-col gap-2.5">
          <LegCard leg={plan.leg_1} label="Leg 1" />
          <LegCard leg={plan.leg_2} label="Leg 2" />
        </div>

        {/* Leverage */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Leverage</p>
          <div className="flex gap-2 mb-3">
            {LEVERAGE_OPTIONS.map((lev) => (
              <button
                key={lev}
                onClick={() => onLeverageChange(lev)}
                className={`px-3 py-1.5 rounded-md text-sm font-mono transition-colors ${
                  leverage === lev
                    ? 'bg-blue-600 text-white'
                    : 'bg-white/[0.04] text-muted-foreground hover:text-foreground hover:bg-white/[0.06]'
                }`}
              >
                {lev}x
              </button>
            ))}
          </div>
          <div className="grid grid-cols-3 gap-3">
            <InfoItem label="Margin Required" value={fmtUsd(plan.leverage.margin_required)} />
            <InfoItem label="Gross Exposure" value={fmtUsd(plan.leverage.gross_exposure)} />
            <InfoItem label="Effective Lev." value={`${plan.leverage.effective_leverage.toFixed(1)}x`} />
          </div>
        </div>

        {/* Trade Summary */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Trade Summary</p>
          <div className="grid grid-cols-2 gap-y-3 gap-x-6">
            <InfoItem label="Direction" value={plan.direction === 'long_a_short_b' ? 'Long A / Short B' : 'Long B / Short A'} />
            <InfoItem label="Target Notional" value={fmtUsd(plan.notional)} />
            <InfoItem label="Expected Entry Cost" value={fmtPct(plan.expected_spread, 4)} />
            <InfoItem label="Est. Net Edge (ann.)" value={fmtPct(plan.estimated_net_edge)} highlight />
          </div>
        </div>

        {/* Bounds */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Execution Bounds</p>
          <div className="grid grid-cols-3 gap-3">
            <BoundItem label="Max Slippage" value={fmtPct(plan.bounds.max_slippage_pct)} />
            <BoundItem label="Max Entry Cost" value={fmtPct(plan.bounds.max_entry_spread_pct)} />
            <BoundItem label="Min Net Edge" value={fmtPct(plan.bounds.min_net_edge_pct)} />
          </div>
        </div>

        {/* Warnings */}
        {plan.warnings && plan.warnings.length > 0 && (
          <div className="px-5 py-4 border-b border-border">
            <ul className="text-sm text-yellow-400 flex flex-col gap-1.5">
              {plan.warnings.map((w, i) => (
                <li key={i} className="flex items-start gap-2 text-[13px]">
                  <span className="mt-0.5 shrink-0">!</span>
                  <span className="text-yellow-400/80">{w}</span>
                </li>
              ))}
            </ul>
          </div>
        )}

        {/* Action */}
        <div className="px-5 py-4 flex flex-col gap-2">
          <Button
            size="lg"
            className="w-full bg-blue-600 hover:bg-blue-500 text-white font-medium"
            disabled={!plan.executable || expired}
            onClick={() => onExecute(plan.opportunity_id)}
          >
            {expired ? 'Plan Expired — Refresh' : plan.executable ? 'Confirm & Execute (Paper)' : 'Not Executable'}
          </Button>
          {expired && (
            <p className="text-[11px] text-muted-foreground text-center">
              Plan expired. A fresh plan will be fetched automatically.
            </p>
          )}
          {!plan.executable && !expired && (
            <p className="text-[11px] text-muted-foreground text-center">
              Requires high confidence on both venues
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

function LegCard({ leg, label }: { leg: Leg; label: string }) {
  const isLong = leg.side === 'long'
  const sideColor = isLong ? 'text-green-400' : 'text-red-400'

  return (
    <div className="flex items-center gap-4 rounded-md bg-white/[0.02] border border-border px-3.5 py-2.5">
      <div className="w-14">
        <p className="text-[11px] text-muted-foreground">{label}</p>
        <span className={`text-sm font-semibold uppercase ${sideColor}`}>{leg.side}</span>
      </div>
      <div className="flex-1">
        <p className="text-[11px] text-muted-foreground">Venue</p>
        <p className="text-sm font-medium capitalize text-foreground">{leg.venue}</p>
      </div>
      <div>
        <p className="text-[11px] text-muted-foreground">Price</p>
        <p className="text-sm font-mono text-foreground">${fmtPrice(leg.expected_price)}</p>
      </div>
      <div className="text-right">
        <p className="text-[11px] text-muted-foreground">Slip+Fee</p>
        <p className="text-sm font-mono text-muted-foreground">{fmtPct(leg.slippage + leg.fee, 3)}</p>
      </div>
    </div>
  )
}

function FreshnessIndicator({ remaining, expired, loading }: { remaining: number; expired: boolean; loading: boolean }) {
  if (loading) {
    return <span className="text-[11px] text-muted-foreground ml-auto">Refreshing...</span>
  }
  if (expired) {
    return (
      <span className="flex items-center gap-1.5 text-[11px] text-yellow-400 ml-auto">
        <span className="size-1.5 rounded-full bg-yellow-400" />
        Expired
      </span>
    )
  }
  return (
    <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground ml-auto">
      <span className="size-1.5 rounded-full bg-green-400" />
      {remaining}s
    </span>
  )
}

function InfoItem({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div>
      <p className="text-[11px] text-muted-foreground">{label}</p>
      <p className={`text-sm font-mono ${highlight ? 'font-semibold text-foreground' : 'text-foreground'}`}>{value}</p>
    </div>
  )
}

function BoundItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md bg-white/[0.03] border border-border px-3 py-2 text-center">
      <p className="text-[11px] text-muted-foreground">{label}</p>
      <p className="text-sm font-mono text-foreground">{value}</p>
    </div>
  )
}
