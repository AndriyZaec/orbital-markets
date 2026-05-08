import { useEffect, useState } from 'react'
import type { Opportunity } from '@/hooks/useOpportunities'
import { Button } from '@/components/ui/button'

interface Props {
  opportunity: Opportunity
  lastUpdated: Date | null
  onClose: () => void
  onOpenSpread: () => void
}

function fmtPct(n: number, decimals = 2) {
  return (n * 100).toFixed(decimals) + '%'
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
    <div className="w-[340px] border-l border-border bg-card flex flex-col shrink-0">
      {/* Header */}
      <div className="px-5 pt-5 pb-4 border-b border-border">
        <div className="flex items-center justify-between">
          <h2 className="text-base font-bold text-foreground">{opp.asset}</h2>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors"
          >
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
              <path d="M11 3L3 11M3 3l8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
            </svg>
          </button>
        </div>
        <div className="flex items-center gap-1.5 mt-1.5">
          <div className={`size-1.5 rounded-full ${isLive ? 'bg-green-400' : 'bg-yellow-400'}`} />
          <span className="text-[11px] text-muted-foreground">
            {isLive ? `Live · ${Math.ceil(countdown)}s` : 'Refreshing...'}
          </span>
        </div>
      </div>

      {/* Scrollable content */}
      <div className="flex-1 overflow-y-auto">
        {/* Sizing */}
        <div className="px-5 py-4 border-b border-border">
          <Row label="Available Notional" value={fmtUsd(opp.available_notional)} />
          <Row label="Recommended" value={fmtUsd(opp.recommended_notional)} />
        </div>

        {/* Execution estimates */}
        <div className="px-5 py-4 border-b border-border">
          <Row label="Entry Cost (est.)" value={fmtPct(opp.entry_spread_estimate, 4)} />
          <Row label="Slippage (est.)" value={fmtPct(opp.slippage_estimate, 4)} />
          <Row label="Fee (est.)" value={fmtPct(opp.fee_estimate, 4)} />
        </div>

        {/* Long leg */}
        <div className="px-5 py-4 border-b border-border">
          <div className="flex items-center gap-2 mb-3">
            <div className="size-2.5 rounded-sm bg-green-400" />
            <span className="text-sm font-semibold text-foreground">Long</span>
            <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="text-green-400">
              <path d="M5 8V2m0 0L2 5m3-3l3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          </div>
          <Row label="Venue" value={longVenue} capitalize />
          <Row label="Funding (h)" value={(longRate * 100).toFixed(4) + '%'} mono />
        </div>

        {/* Short leg */}
        <div className="px-5 py-4 border-b border-border">
          <div className="flex items-center gap-2 mb-3">
            <div className="size-2.5 rounded-sm bg-red-400" />
            <span className="text-sm font-semibold text-foreground">Short</span>
            <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="text-red-400">
              <path d="M5 2v6m0 0l3-3M5 8L2 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          </div>
          <Row label="Venue" value={shortVenue} capitalize />
          <Row label="Funding (h)" value={(shortRate * 100).toFixed(4) + '%'} mono />
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
            Requires high confidence
          </p>
        )}
      </div>
    </div>
  )
}

function Row({ label, value, mono, capitalize }: {
  label: string; value: string; mono?: boolean; capitalize?: boolean
}) {
  return (
    <div className="flex items-center justify-between py-1.5">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className={`text-sm text-foreground ${mono ? 'font-mono' : ''} ${capitalize ? 'capitalize' : ''}`}>
        {value}
      </span>
    </div>
  )
}
