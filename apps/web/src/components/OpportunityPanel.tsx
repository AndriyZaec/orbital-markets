import { useEffect, useState } from 'react'
import type { Opportunity } from '@/hooks/useOpportunities'
import { Button } from '@/components/ui/button'
import { FundingChart } from '@/components/FundingChart'

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
  if (n >= 1_000_000) return '$' + (n / 1_000_000).toFixed(2) + 'm'
  if (n >= 1_000) return '$' + (n / 1_000).toFixed(1) + 'k'
  return '$' + n.toFixed(2)
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

  const isLongA = opp.direction === 'long_a_short_b'
  const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
  const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
  const longRate = isLongA ? opp.funding_rate_a : opp.funding_rate_b
  const shortRate = isLongA ? opp.funding_rate_b : opp.funding_rate_a

  return (
    <div className="w-[420px] border-l border-border bg-card flex flex-col">
      {/* Header */}
      <div className="px-5 pt-5 pb-4 border-b border-border">
        <div className="flex items-center justify-between mb-1">
          <div className="flex items-center gap-3">
            <button
              onClick={onClose}
              className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors -ml-1"
            >
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
                <path d="M9 3L4 7l5 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
              </svg>
            </button>
            <h2 className="text-lg font-bold text-foreground">{opp.asset}</h2>
          </div>
          <div className="flex items-center gap-1.5">
            <div className={`size-1.5 rounded-full ${isLive ? 'bg-green-400' : 'bg-yellow-400'}`} />
            <span className="text-[11px] text-muted-foreground">
              {isLive ? `${Math.ceil(countdown)}s` : 'Refreshing'}
            </span>
          </div>
        </div>
      </div>

      {/* Key Stats Bar */}
      <div className="px-5 py-3 border-b border-border flex items-center gap-5 overflow-x-auto">
        <Stat label="Long" value={longVenue} capitalize className="text-green-400" />
        <Stat label="Short" value={shortVenue} capitalize className="text-red-400" />
        <Stat label="1h Spread" value={fmtRate(opp.funding_spread)} mono />
        <Stat label="APR" value={fmtPct(opp.annualized_gross_edge)} mono />
        <Stat label="Net Edge" value={fmtPct(opp.estimated_net_edge)} mono />
        <Stat label="Notional" value={fmtUsd(opp.available_notional)} mono />
      </div>

      {/* Scrollable content */}
      <div className="flex-1 overflow-y-auto">
        {/* Historical Funding Chart */}
        <div className="px-5 py-4 border-b border-border">
          <FundingChart asset={opp.asset} currentSpread={opp.funding_spread} />
        </div>

        {/* Legs */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Legs</p>
          <div className="flex flex-col gap-2">
            <LegRow label="Long" venue={longVenue} rate={longRate} color="text-green-400" />
            <LegRow label="Short" venue={shortVenue} rate={shortRate} color="text-red-400" />
          </div>
        </div>

        {/* Execution Details */}
        <div className="px-5 py-4 border-b border-border">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Execution</p>
          <div className="grid grid-cols-2 gap-3">
            <InfoItem label="Entry Cost (est.)" value={fmtPct(opp.entry_spread_estimate, 4)} />
            <InfoItem label="Slippage (est.)" value={fmtPct(opp.slippage_estimate, 4)} />
            <InfoItem label="Fee (est.)" value={fmtPct(opp.fee_estimate, 4)} />
            <InfoItem label="Recommended Size" value={fmtUsd(opp.recommended_notional)} />
          </div>
        </div>
      </div>

      {/* Action — sticky bottom */}
      <div className="px-5 py-4 border-t border-border">
        <Button
          className="w-full bg-blue-600 hover:bg-blue-500 text-white font-medium"
          size="lg"
          disabled={!opp.executable}
          onClick={onOpenSpread}
        >
          {opp.executable ? 'Open Spread Trade' : 'Not Executable'}
        </Button>
        {!opp.executable && (
          <p className="text-[11px] text-muted-foreground mt-2 text-center">
            Requires high confidence to execute
          </p>
        )}
      </div>
    </div>
  )
}

function Stat({ label, value, mono, capitalize, className }: {
  label: string; value: string; mono?: boolean; capitalize?: boolean; className?: string
}) {
  return (
    <div className="shrink-0">
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <p className={`text-sm font-medium ${mono ? 'font-mono' : ''} ${capitalize ? 'capitalize' : ''} ${className ?? 'text-foreground'}`}>
        {value}
      </p>
    </div>
  )
}

function InfoItem({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-[11px] text-muted-foreground">{label}</p>
      <p className="text-sm font-mono text-foreground">{value}</p>
    </div>
  )
}

function LegRow({ label, venue, rate, color }: { label: string; venue: string; rate: number; color: string }) {
  return (
    <div className="flex items-center gap-4 rounded-md bg-white/[0.02] border border-border px-3.5 py-2.5">
      <span className={`text-sm font-semibold w-12 ${color}`}>{label}</span>
      <div className="flex-1">
        <p className="text-[11px] text-muted-foreground">Venue</p>
        <p className="text-sm font-medium capitalize text-foreground">{venue}</p>
      </div>
      <div className="text-right">
        <p className="text-[11px] text-muted-foreground">Funding (h)</p>
        <p className="text-sm font-mono text-foreground">{(rate * 100).toFixed(4)}%</p>
      </div>
    </div>
  )
}
