import { useEffect, useState } from 'react'
import type { Opportunity } from '@/hooks/useOpportunities'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'

interface Props {
  opportunity: Opportunity
  lastUpdated: Date | null
  onClose: () => void
  onOpenSpread: () => void
}

function fmtPct(n: number, decimals = 2) {
  return (n * 100).toFixed(decimals) + '%'
}

function fmtRate(n: number) {
  return (n * 100).toFixed(4) + '%'
}

function fmtUsd(n: number) {
  if (n >= 1_000_000) return '$' + (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return '$' + (n / 1_000).toFixed(1) + 'K'
  return '$' + n.toFixed(2)
}

function confidenceVariant(c: Opportunity['confidence']) {
  switch (c) {
    case 'high': return 'default' as const
    case 'medium': return 'secondary' as const
    case 'low': return 'outline' as const
  }
}

function riskColor(r: Opportunity['risk_tier']) {
  switch (r) {
    case 'conservative': return 'text-green-400'
    case 'standard': return 'text-blue-400'
    case 'aggressive': return 'text-yellow-400'
    case 'experimental': return 'text-red-400'
  }
}

function useCountdown(lastUpdated: Date | null, intervalSec: number) {
  const [remaining, setRemaining] = useState(intervalSec)

  useEffect(() => {
    if (!lastUpdated) return
    const update = () => {
      const elapsed = (Date.now() - lastUpdated.getTime()) / 1000
      setRemaining(Math.max(0, intervalSec - elapsed))
    }
    update()
    const id = setInterval(update, 1000)
    return () => clearInterval(id)
  }, [lastUpdated, intervalSec])

  return remaining
}

export function OpportunityPanel({ opportunity: opp, lastUpdated, onClose, onOpenSpread }: Props) {
  const countdown = useCountdown(lastUpdated, 10)
  const isLive = countdown > 0

  const longVenue = opp.direction === 'long_a_short_b' ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
  const shortVenue = opp.direction === 'long_a_short_b' ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
  const longRate = opp.direction === 'long_a_short_b' ? opp.funding_rate_a : opp.funding_rate_b
  const shortRate = opp.direction === 'long_a_short_b' ? opp.funding_rate_b : opp.funding_rate_a

  return (
    <div className="w-[420px] border-l border-border bg-card p-6 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold">{opp.asset}</h2>
          <Badge variant={confidenceVariant(opp.confidence)}>{opp.confidence}</Badge>
          <span className={`text-sm font-medium ${riskColor(opp.risk_tier)}`}>{opp.risk_tier}</span>
        </div>
        <button onClick={onClose} className="text-muted-foreground hover:text-foreground text-lg">×</button>
      </div>

      {/* Freshness indicator */}
      <div className="flex items-center gap-2 mb-5">
        <div className={`size-2 rounded-full ${isLive ? 'bg-green-400' : 'bg-yellow-400'}`} />
        <span className="text-xs text-muted-foreground">
          {isLive ? `Live · refreshes in ${Math.ceil(countdown)}s` : 'Refreshing...'}
        </span>
      </div>

      <Separator className="mb-5" />

      {/* Trade Overview */}
      <div className="grid grid-cols-2 gap-3 mb-5">
        <InfoItem label="Venue Pair" value={`${opp.venue_pair.venue_a} / ${opp.venue_pair.venue_b}`} />
        <InfoItem label="Direction" value={opp.direction === 'long_a_short_b' ? 'Long A / Short B' : 'Long B / Short A'} />
        <InfoItem label="Annualized Gross Edge" value={fmtPct(opp.annualized_gross_edge)} highlight hint="Hourly funding differential, annualized" />
        <InfoItem label="Estimated Net Edge" value={fmtPct(opp.estimated_net_edge)} hint="Gross edge minus estimated entry costs" />
      </div>

      <Separator className="mb-5" />

      {/* Legs */}
      <div className="flex flex-col gap-3 mb-5">
        <LegCard
          label="Leg 1 — Long"
          venue={longVenue}
          side="Long"
          fundingRate={longRate}
        />
        <LegCard
          label="Leg 2 — Short"
          venue={shortVenue}
          side="Short"
          fundingRate={shortRate}
        />
      </div>

      <Separator className="mb-5" />

      {/* Sizing */}
      <h3 className="text-sm font-medium text-muted-foreground mb-3">Sizing</h3>
      <div className="grid grid-cols-2 gap-3 mb-5">
        <InfoItem label="Available Notional" value={fmtUsd(opp.available_notional)} />
        <InfoItem label="Recommended" value={fmtUsd(opp.recommended_notional)} />
      </div>

      <Separator className="mb-5" />

      {/* Costs & Bounds */}
      <h3 className="text-sm font-medium text-muted-foreground mb-3">Execution Bounds</h3>
      <div className="grid grid-cols-2 gap-3 mb-5">
        <InfoItem label="Entry Cost (est.)" value={fmtPct(opp.entry_spread_estimate, 4)} />
        <InfoItem label="Slippage (est.)" value={fmtPct(opp.slippage_estimate, 4)} />
        <InfoItem label="Fee (est.)" value={fmtPct(opp.fee_estimate, 4)} />
        <InfoItem label="Funding Spread (hourly)" value={fmtRate(opp.funding_spread)} />
      </div>

      {/* Warnings */}
      {opp.warnings && opp.warnings.length > 0 && (
        <>
          <Separator className="mb-5" />
          <div className="mb-5">
            <h3 className="text-sm font-medium text-yellow-400 mb-2">Warnings</h3>
            <ul className="text-sm text-muted-foreground flex flex-col gap-1">
              {opp.warnings.map((w, i) => (
                <li key={i} className="flex items-start gap-2">
                  <span className="text-yellow-400 mt-0.5">!</span>
                  {w}
                </li>
              ))}
            </ul>
          </div>
        </>
      )}

      {/* Action */}
      <Button className="w-full" size="lg" disabled={!opp.executable} onClick={onOpenSpread}>
        {opp.executable ? 'Open Spread Trade' : 'Not Executable'}
      </Button>
      {!opp.executable && (
        <p className="text-xs text-muted-foreground mt-2 text-center">
          Requires high confidence to execute
        </p>
      )}
    </div>
  )
}

function InfoItem({ label, value, highlight, hint }: { label: string; value: string; highlight?: boolean; hint?: string }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className={`text-sm font-mono ${highlight ? 'text-foreground font-semibold' : 'text-foreground'}`}>{value}</p>
      {hint && <p className="text-xs text-muted-foreground/60 mt-0.5">{hint}</p>}
    </div>
  )
}

function LegCard({ label, venue, side, fundingRate }: { label: string; venue: string; side: string; fundingRate: number }) {
  return (
    <Card className="bg-muted/50">
      <CardHeader className="pb-2 pt-3 px-4">
        <CardTitle className="text-sm font-medium">{label}</CardTitle>
      </CardHeader>
      <CardContent className="px-4 pb-3">
        <div className="grid grid-cols-3 gap-2 text-sm">
          <div>
            <p className="text-xs text-muted-foreground">Venue</p>
            <p className="font-medium capitalize">{venue}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Side</p>
            <p className={side === 'Long' ? 'text-green-400 font-medium' : 'text-red-400 font-medium'}>{side}</p>
          </div>
          <div>
            <p className="text-xs text-muted-foreground">Funding (h)</p>
            <p className="font-mono">{fmtRate(fundingRate)}</p>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
