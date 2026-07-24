import { useEffect, useState } from 'react'
import type { Opportunity } from '@/hooks/useOpportunities'
import { usePlan } from '@/hooks/usePlan'
import { useLiveExecution } from '@/hooks/useLiveExecution'
import { useVenueReadiness } from '@/hooks/useVenueReadiness'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { LiveExecutionModal } from '@/components/LiveExecutionModal'

interface Props {
  opportunity: Opportunity
  lastUpdated: Date | null
  mode: 'paper' | 'live'
  onClose: () => void
  onExecute: (
    opportunityId: string,
    leverage: number,
    requestedNotional?: number,
  ) => Promise<void>
  onViewPositions?: () => void
  onOpenAccounts?: () => void
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

// Format a backend-provided estimated liquidation price for a leg.
// Backend returns liquidation_price = 0 for 1x (not practically liquidatable).
function fmtLiqPrice(leg: { liquidation_price: number; leverage: number } | null): string {
  if (!leg) return '--'
  if (leg.leverage <= 1 || leg.liquidation_price <= 0) return 'N/A (1x)'
  return fmtPrice(leg.liquidation_price)
}

// Show a real number only when the venue is actually connected AND we have
// a positive balance. When disconnected (or before first snapshot) render
// "--" so we don't misleadingly show $0.00.
function fmtVenueBalance(available: number | null): string {
  if (available === null || !Number.isFinite(available)) return '--'
  if (available <= 0) return '--'
  return fmtUsd(available)
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

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setDebounced(value), delayMs)
    return () => window.clearTimeout(id)
  }, [value, delayMs])
  return debounced
}

const SLIPPAGE_OPTIONS = ['.5%', '1%', '3%', '1'] as const

export function OpportunityPanel({ opportunity: opp, lastUpdated, mode, onClose, onExecute, onViewPositions, onOpenAccounts }: Props) {
  // Matches useOpportunities' 60s poll interval.
  const countdown = useCountdown(lastUpdated, 60)
  const isLive = countdown > 0

  const isLongA = opp.direction === 'long_a_short_b'
  const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
  const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
  const opportunityMaxLev = opp.max_leverage || 1

  const [leverageSelection, setLeverageSelection] = useState({ opportunityId: opp.id, value: opportunityMaxLev })
  const leverage = leverageSelection.opportunityId === opp.id
    ? leverageSelection.value
    : opportunityMaxLev
  const setLeverage = (value: number) => setLeverageSelection({ opportunityId: opp.id, value })
  const [longSlippage, setLongSlippage] = useState(1)
  const [shortSlippage, setShortSlippage] = useState(1)
  const [longOpen, setLongOpen] = useState(true)
  const [shortOpen, setShortOpen] = useState(true)

  // Position size = notional PER LEG. Seeded from the opportunity's suggested
  // notional; user can override. The raw text is kept as a string so partial
  // input ("", "1000.") is not fought by number coercion. `notionalNum` is the
  // parsed numeric value sent to the backend (0 = fall back to recommended).
  const suggestedNotionalInput = opp.recommended_notional > 0 ? String(Math.round(opp.recommended_notional)) : ''
  const [notionalSelection, setNotionalSelection] = useState({ opportunityId: opp.id, value: suggestedNotionalInput })
  const notionalInput = notionalSelection.opportunityId === opp.id
    ? notionalSelection.value
    : suggestedNotionalInput
  const setNotionalInput = (value: string) => setNotionalSelection({ opportunityId: opp.id, value })
  const notionalNum = Number(notionalInput)
  const notionalValid = Number.isFinite(notionalNum) && notionalNum > 0
  const notionalForPlan = notionalValid ? notionalNum : undefined
  const debouncedNotionalForPlan = useDebouncedValue(notionalForPlan, 300)
  const planInputsPending = !Object.is(notionalForPlan, debouncedNotionalForPlan)

  const [executing, setExecuting] = useState(false)
  const { plan, loading: planLoading, error: planError, maxLeverage } = usePlan(opp.id, leverage, debouncedNotionalForPlan)
  const planUpdating = planLoading || planInputsPending
  const maxLev = maxLeverage || opportunityMaxLev
  const { remaining: planRemaining, expired: planExpired } = useExpiry(plan?.expires_at ?? null)

  // Live execution is gated by the typed readiness layer (wallet + signer +
  // balance stream). blockingReasons is already venue-prefixed and de-duped.
  const {
    aggregate: readinessAggregate,
    pacifica: pacReadiness,
    hyperliquid: hlReadiness,
    refreshBalances,
  } = useVenueReadiness()
  const isFullyReady = readinessAggregate.allReady
  const balanceByVenue = (venue: string): number | null => {
    const v = venue.toLowerCase()
    if (v === 'pacifica') return pacReadiness.available
    if (v === 'hyperliquid') return hlReadiness.available
    return null
  }

  // Opening the trade panel is a user intent to trade — nudge a balance
  // refresh so the readiness gate reflects current state rather than the
  // slow (30s) background poll.
  useEffect(() => {
    refreshBalances().catch(() => {})
  }, [refreshBalances])
  const { state: liveState, executeLive, reset: resetLive } = useLiveExecution()
  const [showLiveModal, setShowLiveModal] = useState(false)

  const longLeg = plan ? (plan.leg_1.side === 'long' ? plan.leg_1 : plan.leg_2) : null
  const shortLeg = plan ? (plan.leg_1.side === 'short' ? plan.leg_1 : plan.leg_2) : null

  const handleExecute = async () => {
    setExecuting(true)
    try {
      await onExecute(opp.id, leverage, notionalForPlan)
    } finally {
      setExecuting(false)
    }
  }

  const handleExecuteLive = () => {
    // Kick a balance refresh alongside the execute. Non-blocking: readiness
    // was already ready when the button enabled; this just tightens the
    // window between last-known-fresh and actual submission.
    refreshBalances().catch(() => {})
    setShowLiveModal(true)
    executeLive(opp.id, leverage, notionalForPlan)
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
            <label className={`flex-1 rounded border bg-white/[0.03] px-3 py-2 focus-within:border-blue-500/50 transition-colors ${notionalInput !== '' && !notionalValid ? 'border-red-500/50' : 'border-border'}`}>
              <p className="text-[11px] text-muted-foreground">Position Size (per leg)</p>
              <input
                type="text"
                inputMode="decimal"
                value={notionalInput}
                onChange={(e) => setNotionalInput(e.target.value.replace(/[^\d.]/g, ''))}
                placeholder={opp.recommended_notional > 0 ? String(Math.round(opp.recommended_notional)) : '0'}
                className="w-full bg-transparent text-sm font-mono text-foreground outline-none"
              />
            </label>
            <div className="w-20 rounded border border-border bg-white/[0.03] px-3 py-2 text-center">
              <p className="text-[11px] text-muted-foreground">Currency</p>
              <p className="text-sm text-foreground">USD</p>
            </div>
          </div>
          <div className="mt-1.5 flex items-center justify-between text-[11px]">
            {notionalInput !== '' && !notionalValid ? (
              <span className="text-red-400">Enter a positive amount</span>
            ) : (
              <span className="text-muted-foreground/70">
                Best-price capacity: {fmtUsd(plan?.best_price_capacity ?? opp.best_price_capacity)}
              </span>
            )}
            {opp.recommended_notional > 0 && (
              <button
                type="button"
                onClick={() => setNotionalInput(String(Math.round(opp.recommended_notional)))}
                title="25% of the weaker leg's available best-price liquidity"
                className="text-muted-foreground hover:text-foreground transition-colors"
              >
                Suggested: {fmtUsd(opp.recommended_notional)}
              </button>
            )}
          </div>
        </div>

        {/* Shared leverage */}
        <div className="px-5 py-4 border-b border-border">
          <div className="mb-2">
            <div className="flex items-center justify-between">
              <span className="text-sm text-muted-foreground">Leverage</span>
              <span className="text-[11px] text-muted-foreground/70">Pair max {maxLev}x</span>
            </div>
            {plan && (
              <div className="mt-1 flex items-center justify-between text-[11px] text-muted-foreground/70">
                <span>Margin {fmtUsd(plan.leverage.margin_required)}</span>
                <span>Exposure {fmtUsd(plan.leverage.gross_exposure)}</span>
              </div>
            )}
          </div>
          <LeverageRow
            label={`${longVenue} + ${shortVenue}`}
            value={leverage}
            max={maxLev}
            onChange={setLeverage}
          />
        </div>

        {/* Entry Type */}
        <div className="px-5 py-4 border-b border-border">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm text-muted-foreground">Entry Type</span>
            <span className="text-[10px] text-muted-foreground/70">Market only in v1</span>
          </div>
          <div className="flex items-center gap-0">
            <EntryTypeBtn label="Market" active first />
            <EntryTypeBtn label="Limit" disabled />
            <EntryTypeBtn label="TWAP" disabled last />
          </div>
          <p className="text-[11px] text-muted-foreground/70 mt-2">
            Both legs execute as market orders. Limit and TWAP coming soon.
          </p>
        </div>

        {/* Available balance */}
        <div className="px-5 py-3 border-b border-border">
          <p className="text-[11px] text-muted-foreground mb-1.5">Available balance</p>
          <Row label={longVenue} value={fmtVenueBalance(balanceByVenue(longVenue))} capitalize />
          <Row label={shortVenue} value={fmtVenueBalance(balanceByVenue(shortVenue))} capitalize />
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
                <Row label="Required Margin" value={longLeg ? `${fmtUsd(longLeg.margin_required)} · ${longLeg.leverage}x` : '--'} />
                <Row label="Position Size" value={plan && longLeg ? fmtUsd(plan.notional) : '--'} />
                <Row label="Mid Price" value={longLeg ? fmtPrice(longLeg.expected_price) : '--'} />
                <Row label="Est. Liquidation Price" value={fmtLiqPrice(longLeg)} />
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
                <Row label="Required Margin" value={shortLeg ? `${fmtUsd(shortLeg.margin_required)} · ${shortLeg.leverage}x` : '--'} />
                <Row label="Position Size" value={plan && shortLeg ? fmtUsd(plan.notional) : '--'} />
                <Row label="Mid Price" value={shortLeg ? fmtPrice(shortLeg.expected_price) : '--'} />
                <Row label="Est. Liquidation Price" value={fmtLiqPrice(shortLeg)} />
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
            disabled={!plan?.executable || planExpired || executing || planUpdating || !notionalValid}
            onClick={handleExecute}
          >
            {executing ? 'Executing...' : planUpdating ? 'Loading Plan...' : planExpired ? 'Plan Expired' : opp.execution_status === 'blocked' ? 'Not Executable' : 'Open Paper Trade'}
          </Button>
        ) : (
          <>
            <Button
              className="w-full font-medium"
              size="lg"
              variant={isFullyReady ? 'default' : 'secondary'}
              // When accounts aren't ready, keep the button clickable and
              // route the click to open Connect Accounts. Plan/notional
              // failures still hard-disable (nothing to fix in Accounts).
              disabled={
                isFullyReady
                  ? !plan?.executable || planExpired || planUpdating || !notionalValid
                  : false
              }
              onClick={isFullyReady ? handleExecuteLive : (onOpenAccounts ?? (() => {}))}
            >
              {isFullyReady
                ? 'Execute Live'
                : readinessAggregate.statusLabel === 'Not connected'
                  ? 'Connect Wallets to Go Live'
                  : 'Accounts Not Ready'}
            </Button>
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

function LeverageRow({ label, value, max, onChange }: {
  label: string
  value: number
  max: number
  onChange: (v: number) => void
}) {
  return (
    <div className="mt-2 first:mt-0">
      <div className="flex items-center justify-between mb-1">
        <span className="flex items-center gap-1.5 text-[12px] text-muted-foreground">
          <span className="size-1.5 rounded-full bg-blue-400" />
          {label}
        </span>
        <span className="text-[12px] font-mono text-foreground">
          {value}x
        </span>
      </div>
      <input
        type="range"
        min={1}
        max={max}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full h-1 bg-white/[0.08] rounded-full appearance-none cursor-pointer accent-green-400 [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:size-3.5 [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-foreground [&::-webkit-slider-thumb]:border-2 [&::-webkit-slider-thumb]:border-background"
      />
    </div>
  )
}

function EntryTypeBtn({ label, active, disabled, first, last }: {
  label: string; active?: boolean; disabled?: boolean; first?: boolean; last?: boolean
}) {
  const radius = first ? 'rounded-l' : last ? 'rounded-r' : ''
  const border = first ? 'border' : 'border border-l-0'
  const state = active
    ? 'bg-white/[0.08] text-foreground'
    : disabled
      ? 'bg-white/[0.02] text-muted-foreground/40 cursor-not-allowed'
      : 'bg-white/[0.02] text-muted-foreground hover:text-foreground'
  return (
    <button
      type="button"
      disabled={disabled}
      aria-disabled={disabled}
      aria-pressed={active}
      className={`flex-1 px-3 py-1.5 text-xs font-medium border-border transition-colors ${border} ${radius} ${state}`}
    >
      {label}
      {disabled && <span className="ml-1 text-[9px] uppercase tracking-wide opacity-60">soon</span>}
    </button>
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
