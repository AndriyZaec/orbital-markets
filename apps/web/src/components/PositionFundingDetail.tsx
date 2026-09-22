import { AssetIcon } from '@/components/AssetIcon'
import { FundingChart } from '@/components/FundingChart'
import { useLivePositionDetail } from '@/hooks/useLivePositionDetail'
import type { LivePosition } from '@/hooks/useLivePositions'
import { venueMetadata } from '@/lib/venue-metadata'

interface Props {
  position: LivePosition
  onBack: () => void
}

export function PositionFundingDetail({ position, onBack }: Props) {
  const { data, loading, error, refetch } = useLivePositionDetail(position.id, [position.venue_a, position.venue_b])
  const chartContext = data?.chart_context
  const longVenue = chartContext?.available
    ? (chartContext.direction === 'long_a_short_b' ? chartContext.venue_a : chartContext.venue_b)
    : null
  const shortVenue = chartContext?.available
    ? (chartContext.direction === 'long_a_short_b' ? chartContext.venue_b : chartContext.venue_a)
    : null

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="px-5 pt-4 pb-2 shrink-0">
        <div className="flex items-center gap-2">
          <button onClick={onBack} aria-label="Back to opportunities" className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors -ml-1">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M10 3L5 8l5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>
          <AssetIcon asset={position.asset} />
          <h2 className="text-xl font-bold text-foreground">{position.asset}</h2>
          <span className="rounded border border-blue-400/20 bg-blue-400/[0.07] px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wide text-blue-300">Open position</span>
        </div>
      </div>

      {chartContext?.available && longVenue && shortVenue && (
        <div className="px-5 py-2.5 flex items-center gap-6 border-b border-border shrink-0 overflow-x-auto text-xs">
          <PositionLeg label="Long" venue={longVenue} />
          <PositionLeg label="Short" venue={shortVenue} />
          <div><p className="text-[10px] text-muted-foreground">Position size</p><p className="font-mono text-foreground">${chartContext.notional.toLocaleString()}</p></div>
        </div>
      )}

      <div className="flex-1 overflow-auto min-h-0 px-5 py-4">
        {loading ? (
          <ChartState>Loading position funding history...</ChartState>
        ) : error ? (
          <ChartState>
            <span>{error}</span>
            <button onClick={() => refetch()} className="mt-3 rounded border border-white/10 px-2.5 py-1 text-foreground hover:bg-white/[0.06]">Retry</button>
          </ChartState>
        ) : !chartContext?.available ? (
          <ChartState>{chartContext?.unavailable_reason || 'Funding chart is unavailable for this position.'}</ChartState>
        ) : (
          <>
            {chartContext.projection_warning && (
              <p className="mb-2 text-[10px] text-amber-300/80">{chartContext.projection_warning}</p>
            )}
            <FundingChart
              asset={chartContext.asset}
              venueA={chartContext.venue_a}
              venueB={chartContext.venue_b}
              direction={chartContext.direction}
              currentApr={chartContext.current_apr}
              notional={chartContext.notional}
              feeEstimate={chartContext.fee_estimate}
              slippageEstimate={chartContext.slippage_estimate}
            />
          </>
        )}
      </div>
    </div>
  )
}

function PositionLeg({ label, venue }: { label: string; venue: string }) {
  const metadata = venueMetadata(venue)
  return (
    <div>
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <div className="flex items-center gap-1.5 text-foreground">
        {metadata.logo && <img src={metadata.logo} alt="" className="size-4 rounded-sm" />}
        <span>{metadata.label}</span>
      </div>
    </div>
  )
}

function ChartState({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full min-h-48 flex-col items-center justify-center rounded-lg border border-white/[0.07] bg-[#090d14]/70 px-6 text-center text-xs text-muted-foreground">
      {children}
    </div>
  )
}
