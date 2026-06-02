import { useEffect, useState } from 'react'
import type { Opportunity } from '@/hooks/useOpportunities'
import { usePlan } from '@/hooks/usePlan'
import { useLiveExecution } from '@/hooks/useLiveExecution'
import { useVenueAuthority } from '@/hooks/useVenueAuthority'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { LiveExecutionModal } from '@/components/LiveExecutionModal'
import { getMockLeverage } from '@/lib/hacks'

interface Props {
  opportunity: Opportunity
  lastUpdated: Date | null
  mode: 'paper' | 'live'
  onClose: () => void
  onExecute: (opportunityId: string, leverage: number) => Promise<void>
  onViewPositions?: () => void
}

function fmtPct(n: number, decimals = 4) {
  return (n * 100).toFixed(decimals) + '%'
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

function useExpiry(expiresAt: string | null) {
  const [remaining, setRemaining] = useState(0)
  const [expired, setExpired] = useState(false)

  useEffect(() => {
    if (!expiresAt) return
    const update = () => {
      const ms = new Date(expiresAt).getTime() - Date.now()
      if (ms <= 0) { setRemaining(0); setExpired(true) }
      else { setRemaining(Math.ceil(ms / 1000)); setExpired(false) }
    }
    update()
    const id = setInterval(update, 1000)
    return () => clearInterval(id)
  }, [expiresAt])

  return { remaining, expired }
}

const SLIPPAGE_OPTIONS = ['.5%', '1%', '3%', '1'] as const

export function OpportunityPanel({ opportunity: opp, lastUpdated, mode, onClose, onExecute, onViewPositions }: Props) {
  const countdown = useCountdown(lastUpdated, 10)
  const isLive = countdown > 0

  const isLongA = opp.direction === 'long_a_short_b'
  const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
  const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
  const maxLev = getMockLeverage(opp.asset)

  const [leverageVal, setLeverageVal] = useState(maxLev)
  const [longSlippage, setLongSlippage] = useState(1)
  const [shortSlippage, setShortSlippage] = useState(1)
  const [longOpen, setLongOpen] = useState(true)
  const [shortOpen, setShortOpen] = useState(true)

  const [executing, setExecuting] = useState(false)
  const { plan, loading: planLoading, error: planError } = usePlan(opp.id, leverageVal)
  const { remaining: planRemaining, expired: planExpired } = useExpiry(plan?.expires_at ?? null)

  const { isFullyReady } = useVenueAuthority()
  const { state: liveState, executeLive, reset: resetLive } = useLiveExecution()
  const [showLiveModal, setShowLiveModal] = useState(false)

  const longLeg = plan ? (plan.leg_1.side === 'long' ? plan.leg_1 : plan.leg_2) : null
  const shortLeg = plan ? (plan.leg_1.side === 'short' ? plan.leg_1 : plan.leg_2) : null

  const handleExecute = async () => {
    setExecuting(true)
    try {
      await onExecute(opp.id, leverageVal)
    } finally {
      setExecuting(false)
    }
  }

  const handleExecuteLive = () => {
    setShowLiveModal(true)
    executeLive(opp.id, leverageVal)
  }

  const handleCloseLiveModal = () => {
    setShowLiveModal(false)
    resetLive()
  }

  return (
    <div className="w-[340px] border-l border-border bg-card flex flex-col shrink-0">
      {/* Header */}
      <div className="px-5 pt-5 pb-4 border-b border-border">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <h2 className="text-base font-bold text-foreground">{opp.asset}</h2>
            {plan && (
              <Badge variant={plan.confidence === 'high' ? 'default' : 'secondary'} className="text-[10px]">
                {plan.confidence}
              </Badge>
            )}
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M11 3L3 11M3 3l8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/></svg>
          </button>
        </div>
      </div>

      {/* Scrollable content */}
      <div className="flex-1 overflow-y-auto">
        {/* Position Size + Currency */}
        <div className="px-5 py-4 border-b border-border">
          <div className="flex gap-2">
            <div className="flex-1 rounded border border-border bg-white/[0.03] px-3 py-2">
              <p className="text-[11px] text-muted-foreground">Position Size</p>
              <p className="text-sm font-mono text-foreground">{plan ? fmtUsd(plan.notional) : '--'}</p>
            </div>
            <div className="w-20 rounded border border-border bg-white/[0.03] px-3 py-2 text-center">
              <p className="text-[11px] text-muted-foreground">Currency</p>
              <p className="text-sm text-foreground">USD</p>
            </div>
          </div>
        </div>

        {/* Leverage */}
        <div className="px-5 py-4 border-b border-border">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm text-muted-foreground">Leverage</span>
            <span className="text-sm font-mono text-foreground">{leverageVal}x</span>
          </div>
          <input
            type="range"
            min={1}
            max={maxLev}
            value={leverageVal}
            onChange={(e) => setLeverageVal(Number(e.target.value))}
            className="w-full h-1 bg-white/[0.08] rounded-full appearance-none cursor-pointer accent-green-400 [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:size-3.5 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-foreground [&::-webkit-slider-thumb]:border-2 [&::-webkit-slider-thumb]:border-background"
          />
          {plan && (
            <div className="flex items-center justify-between mt-2 text-[11px] text-muted-foreground">
              <span>Margin: {fmtUsd(plan.leverage.margin_required)}</span>
              <span>Exposure: {fmtUsd(plan.leverage.gross_exposure)}</span>
            </div>
          )}
        </div>

        {/* Available balance */}
        <div className="px-5 py-3 border-b border-border">
          <p className="text-[11px] text-muted-foreground mb-1.5">Available balance</p>
          <Row label={longVenue} value="--" capitalize />
          <Row label={shortVenue} value="--" capitalize />
        </div>

        {/* Long Section */}
        <div className="border-b border-border">
          <button onClick={() => setLongOpen(!longOpen)} className="w-full px-5 py-3 flex items-center justify-between hover:bg-white/[0.02] transition-colors">
            <div className="flex items-center gap-2">
              <div className="size-2.5 rounded-sm bg-green-400" />
              <span className="text-sm font-semibold text-foreground">Long</span>
              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="text-green-400"><path d="M5 8V2m0 0L2.5 4.5M5 2l2.5 2.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/></svg>
            </div>
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none" className={`text-muted-foreground transition-transform ${longOpen ? '' : '-rotate-90'}`}><path d="M3 5l3 3 3-3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>
          {longOpen && (
            <div className="px-5 pb-4">
              <div className="rounded border border-border bg-white/[0.03] px-3 py-2 mb-3">
                <p className="text-sm text-muted-foreground">Market</p>
              </div>
              <SlippageSelector value={longSlippage} onChange={setLongSlippage} />
              <div className="mt-3 flex flex-col gap-0">
                <Row label="Required Margin" value={plan ? fmtUsd(plan.leverage.margin_required / 2) : '--'} />
                <Row label="Position Size" value={plan && longLeg ? fmtUsd(longLeg.expected_price) : '--'} />
                <Row label="Mid Price" value={longLeg ? fmtPrice(longLeg.expected_price) : '--'} />
                <Row label="Est. Liquidation Price" value="--" />
                <Row label="Est. Entry Price" value={longLeg ? fmtPrice(longLeg.expected_price) : '--'} />
                <Row label="Est. Slippage" value={longLeg ? fmtPct(longLeg.slippage + longLeg.fee) : fmtPct(opp.slippage_estimate)} />
              </div>
            </div>
          )}
        </div>

        {/* Short Section */}
        <div className="border-b border-border">
          <button onClick={() => setShortOpen(!shortOpen)} className="w-full px-5 py-3 flex items-center justify-between hover:bg-white/[0.02] transition-colors">
            <div className="flex items-center gap-2">
              <div className="size-2.5 rounded-sm bg-red-400" />
              <span className="text-sm font-semibold text-foreground">Short</span>
              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="text-red-400"><path d="M5 2v6m0 0l2.5-2.5M5 8L2.5 5.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/></svg>
            </div>
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none" className={`text-muted-foreground transition-transform ${shortOpen ? '' : '-rotate-90'}`}><path d="M3 5l3 3 3-3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>
          {shortOpen && (
            <div className="px-5 pb-4">
              <div className="rounded border border-border bg-white/[0.03] px-3 py-2 mb-3">
                <p className="text-sm text-muted-foreground">Market</p>
              </div>
              <SlippageSelector value={shortSlippage} onChange={setShortSlippage} />
              <div className="mt-3 flex flex-col gap-0">
                <Row label="Required Margin" value={plan ? fmtUsd(plan.leverage.margin_required / 2) : '--'} />
                <Row label="Position Size" value={plan && shortLeg ? fmtUsd(shortLeg.expected_price) : '--'} />
                <Row label="Mid Price" value={shortLeg ? fmtPrice(shortLeg.expected_price) : '--'} />
                <Row label="Est. Liquidation Price" value="--" />
                <Row label="Est. Entry Price" value={shortLeg ? fmtPrice(shortLeg.expected_price) : '--'} />
                <Row label="Est. Slippage" value={shortLeg ? fmtPct(shortLeg.slippage + shortLeg.fee) : fmtPct(opp.slippage_estimate)} />
              </div>
            </div>
          )}
        </div>

        {/* Warnings */}
        {plan?.warnings && plan.warnings.length > 0 && (
          <div className="px-5 py-3 border-b border-border">
            <ul className="flex flex-col gap-1">
              {plan.warnings.map((w, i) => (
                <li key={i} className="flex items-start gap-2 text-[12px] text-yellow-400/80">
                  <span className="mt-0.5 shrink-0">!</span>
                  <span>{w}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>

      {/* Action */}
      <div className="px-5 py-4 border-t border-border">
        {planError && (
          <p className="text-[11px] text-red-400 mb-2">Plan error: {planError}</p>
        )}
        <div className="flex items-center gap-1.5 mb-3">
          <div className={`size-1.5 rounded-full ${isLive ? 'bg-green-400' : 'bg-yellow-400'}`} />
          <span className="text-[11px] text-muted-foreground">
            {isLive ? `Live · ${Math.ceil(countdown)}s` : 'Refreshing...'}
          </span>
          {plan && !planExpired && (
            <span className="text-[11px] text-muted-foreground ml-auto">
              Plan: {planRemaining}s
            </span>
          )}
          {plan && planExpired && (
            <span className="text-[11px] text-yellow-400 ml-auto">Plan expired</span>
          )}
        </div>

        {mode === 'paper' ? (
          <Button
            className="w-full bg-blue-600 hover:bg-blue-500 text-white font-medium"
            size="lg"
            disabled={!plan?.executable || planExpired || executing || planLoading}
            onClick={handleExecute}
          >
            {executing ? 'Executing...' : planLoading ? 'Loading Plan...' : planExpired ? 'Plan Expired' : opp.execution_status === 'blocked' ? 'Not Executable' : 'Open Paper Trade'}
          </Button>
        ) : (
          <>
            <Button
              className="w-full font-medium"
              size="lg"
              variant={isFullyReady ? 'default' : 'secondary'}
              disabled={!isFullyReady || !plan?.executable || planExpired || planLoading}
              onClick={handleExecuteLive}
            >
              {isFullyReady ? 'Execute Live' : 'Connect Wallets to Go Live'}
            </Button>
            {!isFullyReady && (
              <p className="text-[10px] text-muted-foreground/60 text-center mt-1.5">Connect both venue accounts to enable live execution</p>
            )}
          </>
        )}
      </div>

      {showLiveModal && (
        <LiveExecutionModal
          state={liveState}
          onRetry={handleExecuteLive}
          onClose={handleCloseLiveModal}
          onViewPositions={() => { handleCloseLiveModal(); onViewPositions?.() }}
        />
      )}
    </div>
  )
}

function Row({ label, value, capitalize }: { label: string; value: string; capitalize?: boolean }) {
  return (
    <div className="flex items-center justify-between py-1.5">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className={`text-sm font-mono text-foreground ${capitalize ? 'capitalize' : ''}`}>{value}</span>
    </div>
  )
}

function SlippageSelector({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  return (
    <div className="flex items-center gap-0">
      <div className="flex-1 rounded-l border border-border bg-white/[0.03] px-3 py-1.5">
        <span className="text-sm text-muted-foreground">Slippage</span>
      </div>
      {SLIPPAGE_OPTIONS.map((opt, i) => {
        const isActive = i === value
        return (
          <button
            key={opt}
            onClick={() => onChange(i)}
            className={`px-2.5 py-1.5 text-xs font-medium border border-l-0 border-border transition-colors ${
              i === SLIPPAGE_OPTIONS.length - 1 ? 'rounded-r' : ''
            } ${isActive ? 'bg-white/[0.08] text-foreground' : 'bg-white/[0.02] text-muted-foreground hover:text-foreground'}`}
          >
            {opt}
          </button>
        )
      })}
    </div>
  )
}
