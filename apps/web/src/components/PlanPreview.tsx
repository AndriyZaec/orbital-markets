import { useEffect, useState } from 'react'
import type { ExecutionPlan, Leg } from '@/hooks/usePlan'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'

interface Props {
  plan: ExecutionPlan
  loading: boolean
  error: string | null
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

export function PlanPreview({ plan, loading, error, onClose, onExecute }: Props) {
  const { remaining, expired } = useExpiry(plan.expires_at)

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50" onClick={onClose}>
      <div
        className="bg-card border border-border rounded-lg w-[520px] max-h-[90vh] overflow-y-auto shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-6 pt-5 pb-4">
          <div className="flex items-center justify-between mb-1">
            <h2 className="text-lg font-semibold">Execution Plan</h2>
            <button onClick={onClose} className="text-muted-foreground hover:text-foreground text-lg">×</button>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-2xl font-bold">{plan.asset}</span>
            <Badge variant={plan.confidence === 'high' ? 'default' : 'secondary'}>{plan.confidence}</Badge>
            <FreshnessIndicator remaining={remaining} expired={expired} loading={loading} />
          </div>
        </div>

        <Separator />

        {/* Error */}
        {error && (
          <div className="px-6 py-3 bg-destructive/10 text-destructive text-sm">
            Plan refresh failed: {error}
          </div>
        )}

        {/* Legs */}
        <div className="px-6 py-4 flex flex-col gap-3">
          <LegCard leg={plan.leg_1} label="Leg 1" />
          <LegCard leg={plan.leg_2} label="Leg 2" />
        </div>

        <Separator />

        {/* Spread & Edge */}
        <div className="px-6 py-4">
          <h3 className="text-sm font-medium text-muted-foreground mb-3">Trade Summary</h3>
          <div className="grid grid-cols-2 gap-y-3 gap-x-6">
            <InfoItem label="Direction" value={plan.direction === 'long_a_short_b' ? 'Long A / Short B' : 'Long B / Short A'} />
            <InfoItem label="Target Notional" value={fmtUsd(plan.notional)} />
            <InfoItem label="Expected Spread" value={fmtPct(plan.expected_spread, 4)} />
            <InfoItem label="Est. Net Edge (ann.)" value={fmtPct(plan.estimated_net_edge)} highlight />
          </div>
        </div>

        <Separator />

        {/* Bounds */}
        <div className="px-6 py-4">
          <h3 className="text-sm font-medium text-muted-foreground mb-3">Execution Bounds</h3>
          <div className="grid grid-cols-3 gap-3">
            <BoundItem label="Max Slippage" value={fmtPct(plan.bounds.max_slippage_pct)} />
            <BoundItem label="Max Entry Spread" value={fmtPct(plan.bounds.max_entry_spread_pct)} />
            <BoundItem label="Min Net Edge" value={fmtPct(plan.bounds.min_net_edge_pct)} />
          </div>
        </div>

        {/* Warnings */}
        {plan.warnings && plan.warnings.length > 0 && (
          <>
            <Separator />
            <div className="px-6 py-4">
              <ul className="text-sm text-yellow-400 flex flex-col gap-1">
                {plan.warnings.map((w, i) => (
                  <li key={i} className="flex items-start gap-2">
                    <span className="mt-0.5">⚠</span>
                    {w}
                  </li>
                ))}
              </ul>
            </div>
          </>
        )}

        <Separator />

        {/* Action */}
        <div className="px-6 py-4 flex flex-col gap-2">
          <Button
            size="lg"
            className="w-full"
            disabled={!plan.executable || expired}
            onClick={() => onExecute(plan.opportunity_id)}
          >
            {expired ? 'Plan Expired — Refresh' : plan.executable ? 'Confirm & Execute (Paper)' : 'Not Executable'}
          </Button>
          {expired && (
            <p className="text-xs text-muted-foreground text-center">
              Plan expired. A fresh plan will be fetched automatically.
            </p>
          )}
          {!plan.executable && !expired && (
            <p className="text-xs text-muted-foreground text-center">
              Requires high confidence on both venues
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

function LegCard({ leg, label }: { leg: Leg; label: string }) {
  const sideColor = leg.side === 'long' ? 'text-green-400' : 'text-red-400'
  const sideBg = leg.side === 'long' ? 'border-green-500/20' : 'border-red-500/20'

  return (
    <Card className={`bg-muted/50 border ${sideBg}`}>
      <CardHeader className="pb-2 pt-3 px-4">
        <CardTitle className="text-sm font-medium flex items-center justify-between">
          <span>{label}</span>
          <span className={`text-xs font-semibold uppercase ${sideColor}`}>{leg.side}</span>
        </CardTitle>
      </CardHeader>
      <CardContent className="px-4 pb-3">
        <div className="grid grid-cols-3 gap-3 text-sm">
          <div>
            <p className="text-xs text-muted-foreground">Venue</p>
            <p className="font-medium capitalize">{leg.venue}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Expected Price</p>
            <p className="font-mono">${fmtPrice(leg.expected_price)}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Slip + Fee</p>
            <p className="font-mono">{fmtPct(leg.slippage + leg.fee, 3)}</p>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function FreshnessIndicator({ remaining, expired, loading }: { remaining: number; expired: boolean; loading: boolean }) {
  if (loading) {
    return <span className="text-xs text-muted-foreground ml-auto">Refreshing...</span>
  }
  if (expired) {
    return (
      <span className="flex items-center gap-1.5 text-xs text-yellow-400 ml-auto">
        <span className="size-2 rounded-full bg-yellow-400" />
        Expired
      </span>
    )
  }
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted-foreground ml-auto">
      <span className="size-2 rounded-full bg-green-400" />
      Live · {remaining}s
    </span>
  )
}

function InfoItem({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className={`text-sm font-mono ${highlight ? 'font-semibold text-foreground' : ''}`}>{value}</p>
    </div>
  )
}

function BoundItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md bg-muted px-3 py-2 text-center">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="text-sm font-mono">{value}</p>
    </div>
  )
}
