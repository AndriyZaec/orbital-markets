import { lazy, memo, Suspense, useState, useMemo, useEffect, useCallback, useLayoutEffect, useRef } from 'react'
import { apiError, apiFetch, userErrorMessage } from '@/lib/api'
import { useOpportunities } from '@/hooks/useOpportunities'

import type { Opportunity, OpportunitySignal, OpportunitySignalStatus } from '@/hooks/useOpportunities'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { AssetIcon } from '@/components/AssetIcon'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { InfoIcon, SearchIcon, XIcon } from 'lucide-react'

import { LivePositions } from '@/components/LivePositions'
import { useVenueReadiness } from '@/hooks/useVenueReadiness'
import { DetailStatItem, DetailVenueIcon } from '@/components/DetailStatItem'
import { DeferredBoundary } from '@/components/DeferredBoundary'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import type { LivePosition } from '@/hooks/useLivePositions'
import { venueMetadata } from '@/lib/venue-metadata'
import { enforceMinimumVenueSelection, matchesVenueFilter } from '@/lib/opportunity-filters'
import { knownMaxLeverage } from '@/lib/leverage'

type View = 'trade' | 'portfolio'
type TradeSelection =
  | { kind: 'opportunity'; id: string }
  | { kind: 'position'; position: LivePosition }
  | null
type SortField = 'asset' | 'apr' | 'aprMaxLev' | 'priceSpread' | 'oi' | 'capacity' | 'fundingSpread' | 'pacificaRate' | 'hlRate' | 'signal7d'
type SortDir = 'asc' | 'desc'

const FILTER_VENUES = ['pacifica', 'hyperliquid', 'aster'] as const

const OpportunityPanel = lazy(() => import('@/components/OpportunityPanel').then((module) => ({ default: module.OpportunityPanel })))
const LivePositionPanel = lazy(() => import('@/components/LivePositionDetail').then((module) => ({ default: module.LivePositionPanel })))
const PositionFundingDetail = lazy(() => import('@/components/PositionFundingDetail').then((module) => ({ default: module.PositionFundingDetail })))
const Portfolio = lazy(() => import('@/components/Portfolio').then((module) => ({ default: module.Portfolio })))
const ConnectAccounts = lazy(() => import('@/components/ConnectAccounts').then((module) => ({ default: module.ConnectAccounts })))
const FundingChart = lazy(() => import('@/components/FundingChart').then((module) => ({ default: module.FundingChart })))

function DeferredSurface({ label }: { label: string }) {
  return <div className="flex h-full w-full items-center justify-center text-xs text-muted-foreground">{label}</div>
}

// Per-venue raw funding rate (single funding period, signed).
// venue_a / venue_b naming is opaque; we look up by venue name so columns
// stay aligned to the actual venue regardless of which slot it lands in.
function fundingForVenue(opp: Opportunity, venue: string): number | null {
  const v = venue.toLowerCase()
  if (opp.venue_pair.venue_a.toLowerCase() === v) return opp.funding_rate_a
  if (opp.venue_pair.venue_b.toLowerCase() === v) return opp.funding_rate_b
  return null
}

function fmtPct(n: number, decimals = 2) {
  return (n * 100).toFixed(decimals) + '%'
}

function fmtRate(n: number) {
  return (n * 100).toFixed(4) + '%'
}

function fmtUsd(n: number) {
  if (n >= 1_000_000_000) return '$' + (n / 1_000_000_000).toFixed(2) + 'b'
  if (n >= 1_000_000) return '$' + (n / 1_000_000).toFixed(2) + 'm'
  if (n >= 1_000) return '$' + (n / 1_000).toFixed(2) + 'k'
  return '$' + n.toFixed(2)
}

function getSortValue(opp: Opportunity, field: SortField): number | string {
  switch (field) {
    case 'asset': return opp.asset
    case 'apr': return opp.annualized_gross_edge
    case 'aprMaxLev': return opp.max_leverage > 0 ? opp.annualized_gross_edge * opp.max_leverage : 0
    case 'priceSpread': return opp.entry_spread_estimate
    case 'oi': return opp.available_notional
    case 'capacity': return opp.best_price_capacity
    case 'fundingSpread': return Math.abs(opp.funding_spread)
    case 'pacificaRate': return fundingForVenue(opp, 'pacifica') ?? 0
    case 'hlRate': return fundingForVenue(opp, 'hyperliquid') ?? 0
    case 'signal7d': return 0
  }
}

const signalSortRank: Record<OpportunitySignalStatus, number> = {
  persistent: 7,
  intermittent: 6,
  new: 5,
  choppy: 4,
  reversed: 3,
  faded: 2,
  flat: 1,
  limited: 0,
}

function compareOpportunitySignals(a: OpportunitySignal | null, b: OpportunitySignal | null) {
  if (a === null) return b === null ? 0 : 1
  if (b === null) return -1
  return signalSortRank[a.status] - signalSortRank[b.status]
    || a.activity - b.activity
    || a.average_edge - b.average_edge
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

function OrbitalLogo() {
  return (
    <span className="orbital-logo" aria-hidden="true">
      <i />
      <i />
      <b />
    </span>
  )
}

function opportunityIdFromURL() {
  return new URLSearchParams(window.location.search).get('opportunity')
}

function matchesOpportunityId(opportunity: Opportunity, id: string | null) {
  if (!id || opportunity.id === id) return opportunity.id === id
  const { venue_a: venueA, venue_b: venueB } = opportunity.venue_pair
  return [
    `${opportunity.asset}-${venueA}-${venueB}-long_a_short_b`,
    `${opportunity.asset}-${venueA}-${venueB}-long_b_short_a`,
    `${opportunity.asset}-${venueB}-${venueA}-long_a_short_b`,
    `${opportunity.asset}-${venueB}-${venueA}-long_b_short_a`,
  ].includes(id)
}

export default function App() {
  const [activeView, setActiveView] = useState<View>(() => (
    'trade'
  ))
  const { aggregate: accountsAggregate, aster: asterReadiness } = useVenueReadiness()
  const opportunityAccounts = asterReadiness.address ? { aster: asterReadiness.address } : undefined
  const { opportunities, loading, error, lastUpdated } = useOpportunities(opportunityAccounts)
  const [selection, setSelection] = useState<TradeSelection>(() => {
    const id = opportunityIdFromURL()
    return id ? { kind: 'opportunity', id } : null
  })
  const [focusPositionId, setFocusPositionId] = useState<string | null>(null)
  const [opportunityQuery, setOpportunityQuery] = useState('')
  const [showAccounts, setShowAccounts] = useState(false)
  // Header account status is driven by the same typed readiness layer used
  // by Connect Accounts and Execute Live — one source of truth for the
  // "is this trader actually ready to trade" signal.
  const tradingMode = 'live' as const
  // Matches useOpportunities' 60s poll interval — scanner refreshes every 60s.
  const countdown = useCountdown(lastUpdated, 60)
  const isLive = countdown > 0

  const selectedReferenceId = selection?.kind === 'opportunity' ? selection.id : null
  const selectedPosition = selection?.kind === 'position' ? selection.position : null
  const selected = opportunities.find((opportunity) => matchesOpportunityId(opportunity, selectedReferenceId)) ?? null
  const selectedId = selected?.id ?? selectedReferenceId
  const suggestedNotionalInput = selected?.recommended_notional
    ? String(Math.round(selected.recommended_notional))
    : ''
  const [notionalSelection, setNotionalSelection] = useState({ opportunityId: '', value: '' })
  const selectedNotionalInput = selected && notionalSelection.opportunityId === selected.id
    ? notionalSelection.value
    : suggestedNotionalInput
  const selectedNotional = Number(selectedNotionalInput)
  const projectionNotional = Number.isFinite(selectedNotional) && selectedNotional > 0
    ? selectedNotional
    : 0
  const setSelectedNotionalInput = useCallback((value: string) => {
    if (selectedId) setNotionalSelection({ opportunityId: selectedId, value })
  }, [selectedId])

  const selectOpportunity = useCallback((id: string) => {
    const url = new URL(window.location.href)
    url.searchParams.set('opportunity', id)
    window.history.pushState({ ...window.history.state, orbitalOpportunity: id }, '', url)
    setSelection({ kind: 'opportunity', id })
  }, [])

  const selectPosition = useCallback((position: LivePosition | null) => {
    if (!position) {
      setSelection((current) => current?.kind === 'position' ? null : current)
      return
    }
    setFocusPositionId(null)
    const url = new URL(window.location.href)
    url.searchParams.delete('opportunity')
    const historyState = { ...(window.history.state ?? {}) }
    delete historyState.orbitalOpportunity
    window.history.replaceState(historyState, '', url)
    setSelection({ kind: 'position', position })
  }, [])

  const closeOpportunity = useCallback(() => {
    if (window.history.state?.orbitalOpportunity === selectedId) {
      window.history.back()
      return
    }
    const url = new URL(window.location.href)
    url.searchParams.delete('opportunity')
    window.history.replaceState(window.history.state, '', url)
    setSelection(null)
  }, [selectedId])

  useEffect(() => {
    const handlePopState = () => {
      const id = opportunityIdFromURL()
      setSelection(id ? { kind: 'opportunity', id } : null)
      setActiveView('trade')
    }
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useEffect(() => {
    if (window.location.pathname !== '/') {
      window.history.replaceState(window.history.state, '', '/')
    }
  }, [activeView])

  // Resizable positions panel
  const [posHeight, setPosHeight] = useState(280)
  const dragging = useRef(false)
  const startY = useRef(0)
  const startH = useRef(280)

  const onResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    dragging.current = true
    startY.current = e.clientY
    startH.current = posHeight
    const onMove = (ev: MouseEvent) => {
      if (!dragging.current) return
      const delta = startY.current - ev.clientY
      const maxH = window.innerHeight * 0.6
      setPosHeight(Math.max(120, Math.min(maxH, startH.current + delta)))
    }
    const onUp = () => {
      dragging.current = false
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }
    document.body.style.cursor = 'row-resize'
    document.body.style.userSelect = 'none'
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
  }, [posHeight])

  const handleExecutePaper = async (
    opportunityId: string,
    leverage: number,
    requestedNotional?: number,
  ) => {
    try {
      const body: Record<string, unknown> = {
        opportunity_id: opportunityId,
        leverage,
      }
      if (typeof requestedNotional === 'number' && requestedNotional > 0) {
        body.requested_notional = requestedNotional
      }
      const resp = await apiFetch('/api/v1/paper/open', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}))
        alert(apiError(resp.status, 'Unable to open the position. Please try again.', body).message)
        return
      }
      closeOpportunity()
    } catch (e) {
      alert(userErrorMessage(e, 'Unable to open the position. Please try again.'))
    }
  }

  return (
    <div className="dark h-screen bg-background flex flex-col overflow-hidden">
      <header className="h-12 border-b border-border flex items-center px-5 shrink-0">
        <button className="orbital-brand flex items-center gap-2.5 mr-10 cursor-pointer" onClick={() => { closeOpportunity(); setActiveView('trade') }}>
          <OrbitalLogo />
          {/* Beta tag sits as a superscript on the wordmark — reads as
              "Orbital Markets ᵇᵉᵗᵃ", visually anchored to the brand rather
              than mixed in with the account controls on the right. */}
          <span className="relative text-[15px] font-semibold tracking-tight text-foreground">
            Orbital Markets
            <span
              title="Closed beta — real venues, real signatures."
              className="absolute -top-1 -right-8 text-[8px] font-medium uppercase tracking-wider text-yellow-400/90 border border-yellow-400/30 rounded px-1 py-px leading-none"
            >
              Beta
            </span>
          </span>
        </button>
        <nav className="flex items-center gap-1">
          <NavBtn active={activeView === 'trade'} onClick={() => setActiveView('trade')}>Trade</NavBtn>
          <NavBtn active={activeView === 'portfolio'} onClick={() => setActiveView('portfolio')}>Portfolio</NavBtn>
          <MarketingNavBtn>Fee Rebates</MarketingNavBtn>
          <MarketingNavBtn>For Agents</MarketingNavBtn>
        </nav>
        <div className="ml-auto flex items-center gap-4">
          {/* Refresh countdown */}
          <div className="flex items-center gap-1.5">
            <div className={`size-1.5 rounded-full ${isLive ? 'bg-green-400' : 'bg-yellow-400'}`} />
            <span className="text-xs text-muted-foreground font-mono">
              {isLive ? `${Math.ceil(countdown)}s` : '...'}
            </span>
          </div>
          <AccountsHeaderButton
            aggregate={accountsAggregate}
            open={showAccounts}
            onClick={() => setShowAccounts((v) => !v)}
          />
        </div>
      </header>

      <div className="flex-1 flex min-h-0 bg-[#080b12] overflow-hidden">
        <div className="flex-1 flex flex-col min-w-0 min-h-0">
          {activeView === 'trade' && (
            <>
              <div className="flex-1 flex flex-col min-h-0 bg-[#080b12]">
                {selectedPosition ? (
                  <DeferredBoundary label="Position history">
                    <Suspense fallback={<DeferredSurface label="Loading position history..." />}>
                      <PositionFundingDetail
                        position={selectedPosition}
                        onBack={() => setSelection(null)}
                      />
                    </Suspense>
                  </DeferredBoundary>
                ) : selected ? (
                  <OpportunityDetail
                    opportunity={selected}
                    notional={projectionNotional}
                    onNotionalChange={(value) => setSelectedNotionalInput(String(value))}
                    onBack={closeOpportunity}
                  />
                ) : (
                  <OpportunityTable
                    opportunities={opportunities}
                    loading={loading}
                    error={error}
                    query={opportunityQuery}
                    onQueryChange={setOpportunityQuery}
                    onSelect={selectOpportunity}
                  />
                )}
              </div>
              <div className="shrink-0 border-t border-border flex flex-col min-h-0 relative bg-[#080b12]" style={{ height: posHeight }}>
                <div
                  className="absolute top-0 left-0 right-0 h-1.5 cursor-row-resize z-10 hover:bg-blue-500/20 transition-colors"
                  onMouseDown={onResizeStart}
                />
                <LivePositions
                  onConnectWallets={() => setShowAccounts(true)}
                  onSelectPosition={selectPosition}
                  selectedPositionId={selectedPosition?.id}
                  focusPositionId={focusPositionId}
                />
              </div>
            </>
          )}

          {activeView === 'portfolio' && (
            <PageBg>
              <DeferredBoundary label="Portfolio">
                <Suspense fallback={<DeferredSurface label="Loading portfolio..." />}>
                  <Portfolio
                    onConnectWallets={() => setShowAccounts(true)}
                    onViewPositions={() => setActiveView('trade')}
                  />
                </Suspense>
              </DeferredBoundary>
            </PageBg>
          )}

        </div>

        {activeView === 'trade' && selected && !selectedPosition && (
          <DeferredBoundary label="Execution panel">
            <Suspense fallback={<DeferredSurface label="Loading execution panel..." />}>
              <OpportunityPanel
                opportunity={selected}
                lastUpdated={lastUpdated}
                mode={tradingMode}
                notionalInput={selectedNotionalInput}
                onNotionalInputChange={setSelectedNotionalInput}
                onClose={closeOpportunity}
                onExecute={handleExecutePaper}
                onViewPositions={(positionId) => {
                  if (positionId) setFocusPositionId(positionId)
                  else closeOpportunity()
                }}
                onOpenAccounts={() => setShowAccounts(true)}
              />
            </Suspense>
          </DeferredBoundary>
        )}

        {activeView === 'trade' && selectedPosition && (
          <DeferredBoundary label="Position controls">
            <Suspense fallback={<DeferredSurface label="Loading position controls..." />}>
              <LivePositionPanel
                position={selectedPosition}
                onClose={() => setSelection(null)}
              />
            </Suspense>
          </DeferredBoundary>
        )}

        {showAccounts && (
          <DeferredBoundary label="Account management">
            <Suspense fallback={<DeferredSurface label="Loading account management..." />}>
              <ConnectAccounts open onClose={() => setShowAccounts(false)} />
            </Suspense>
          </DeferredBoundary>
        )}
      </div>

    </div>
  )
}

/* ── Opportunity Table ─────────────────────────────────── */

function OpportunityTable({ opportunities, loading, error, query, onQueryChange, onSelect }: {
  opportunities: Opportunity[]
  loading: boolean
  error: string | null
  query: string
  onQueryChange: (query: string) => void
  onSelect: (id: string) => void
}) {
  const [sortField, setSortField] = useState<SortField>('apr')
  const [sortDir, setSortDir] = useState<SortDir>('desc')
  const [selectedVenues, setSelectedVenues] = useState<string[]>([...FILTER_VENUES])
  const [orderedIds, setOrderedIds] = useState<string[]>([])
  const orderRef = useRef<string[]>([])
  const rowRefs = useRef(new Map<string, HTMLTableRowElement>())
  const previousRowPositions = useRef(new Map<string, number>())
  const pendingOrder = useRef<string[] | null>(null)
  const reorderTimer = useRef<number | null>(null)
  const interactionIdleTimer = useRef<number | null>(null)
  const tableViewportRef = useRef<HTMLDivElement>(null)
  const pointerInside = useRef(false)
  const focusInside = useRef(false)
  const scrolling = useRef(false)

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortDir(sortDir === 'desc' ? 'asc' : 'desc')
    } else {
      setSortField(field)
      setSortDir('desc')
    }
  }

  const filtered = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    return opportunities.filter((opportunity) => (
      matchesVenueFilter(opportunity.venue_pair, selectedVenues)
      && (!normalizedQuery || opportunity.asset.toLowerCase().includes(normalizedQuery))
    ))
  }, [opportunities, query, selectedVenues])

  const sorted = useMemo(() => {
    if (filtered.length === 0) return filtered
    return [...filtered].sort((a, b) => {
      if (sortField === 'signal7d') {
        const comparison = compareOpportunitySignals(a.signal_7d, b.signal_7d)
        if (a.signal_7d === null || b.signal_7d === null) return comparison
        return sortDir === 'desc' ? -comparison : comparison
      }
      const va = getSortValue(a, sortField)
      const vb = getSortValue(b, sortField)
      const cmp = typeof va === 'string' ? va.localeCompare(vb as string) : (va as number) - (vb as number)
      return sortDir === 'desc' ? -cmp : cmp
    })
  }, [filtered, sortField, sortDir])

  const applyOrder = useCallback((ids: string[]) => {
    if (ids.length === orderRef.current.length && ids.every((id, index) => id === orderRef.current[index])) {
      pendingOrder.current = null
      return
    }
    previousRowPositions.current = new Map(
      [...rowRefs.current].map(([id, row]) => [id, row.getBoundingClientRect().top]),
    )
    orderRef.current = ids
    pendingOrder.current = null
    setOrderedIds(ids)
  }, [])

  const flushPendingOrder = useCallback(() => {
    focusInside.current = tableViewportRef.current?.contains(document.activeElement) ?? false
    if (pointerInside.current || focusInside.current || scrolling.current || !pendingOrder.current) return
    applyOrder(pendingOrder.current)
  }, [applyOrder])

  const sortedIds = useMemo(() => sorted.map((opportunity) => opportunity.id), [sorted])
  const controlsKey = `${sortField}\u0000${sortDir}\u0000${query.trim().toLowerCase()}\u0000${selectedVenues.join(',')}`
  const previousControlsKey = useRef(controlsKey)

  useEffect(() => {
    if (reorderTimer.current !== null) window.clearTimeout(reorderTimer.current)
    const controlsChanged = previousControlsKey.current !== controlsKey
    previousControlsKey.current = controlsKey

    if (orderRef.current.length === 0 || controlsChanged) {
      pendingOrder.current = sortedIds
      reorderTimer.current = window.setTimeout(() => applyOrder(sortedIds), 0)
      return () => {
        if (reorderTimer.current !== null) window.clearTimeout(reorderTimer.current)
      }
    }
    if (sortedIds.length === orderRef.current.length && sortedIds.every((id, index) => id === orderRef.current[index])) {
      pendingOrder.current = null
      return
    }

    pendingOrder.current = sortedIds
    reorderTimer.current = window.setTimeout(flushPendingOrder, 500)
    return () => {
      if (reorderTimer.current !== null) window.clearTimeout(reorderTimer.current)
    }
  }, [applyOrder, controlsKey, flushPendingOrder, sortedIds])

  useLayoutEffect(() => {
    const previous = previousRowPositions.current
    previousRowPositions.current = new Map()
    if (previous.size === 0 || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    for (const [id, row] of rowRefs.current) {
      const oldTop = previous.get(id)
      if (oldTop === undefined) continue
      const delta = oldTop - row.getBoundingClientRect().top
      if (Math.abs(delta) < 1) continue
      row.animate(
        [{ transform: `translateY(${delta}px)` }, { transform: 'translateY(0)' }],
        { duration: 280, easing: 'cubic-bezier(0.22, 1, 0.36, 1)' },
      )
    }
  }, [orderedIds])

  useEffect(() => () => {
    if (reorderTimer.current !== null) window.clearTimeout(reorderTimer.current)
    if (interactionIdleTimer.current !== null) window.clearTimeout(interactionIdleTimer.current)
  }, [])

  const displayed = useMemo(() => {
    if (orderedIds.length === 0) return sorted
    const byId = new Map(filtered.map((opportunity) => [opportunity.id, opportunity]))
    const ordered = orderedIds.flatMap((id) => {
      const opportunity = byId.get(id)
      return opportunity ? [opportunity] : []
    })
    const knownIds = new Set(orderedIds)
    return [...ordered, ...sorted.filter((opportunity) => !knownIds.has(opportunity.id))]
  }, [filtered, orderedIds, sorted])

  const finishInteraction = useCallback(() => {
    if (interactionIdleTimer.current !== null) window.clearTimeout(interactionIdleTimer.current)
    interactionIdleTimer.current = window.setTimeout(() => {
      if (!pointerInside.current && !focusInside.current && !scrolling.current) flushPendingOrder()
    }, 400)
  }, [flushPendingOrder])

  const handleTableScroll = useCallback(() => {
    scrolling.current = true
    if (interactionIdleTimer.current !== null) window.clearTimeout(interactionIdleTimer.current)
    interactionIdleTimer.current = window.setTimeout(() => {
      scrolling.current = false
      if (!pointerInside.current && !focusInside.current) flushPendingOrder()
    }, 400)
  }, [flushPendingOrder])

  return (
    <>
      {loading ? (
        <div className="flex-1 flex flex-col items-center justify-center bg-[#080b12]">
          <div className="animate-[loader-pulse_2s_ease-in-out_infinite]">
            <OrbitalLogo />
          </div>
          <p className="text-muted-foreground text-xs mt-3">Scanning opportunities...</p>
        </div>
      ) : (<>
      <div className="shrink-0 bg-[#080b12] px-5 pb-3 pt-5">
        <div className="flex items-center justify-between gap-4">
          <h2 className="text-base font-bold text-foreground">Opportunities</h2>
          <span className="rounded-full border border-white/[0.07] bg-white/[0.03] px-2 py-0.5 text-[10px] font-mono text-muted-foreground">
            {displayed.length} live
          </span>
        </div>
      </div>
      <div role="search" className="shrink-0 border-y border-border/70 bg-card/25 px-5 py-2">
        <div className="flex flex-col gap-2 md:flex-row md:items-center">
          <div className="w-full md:max-w-xs">
            <label htmlFor="opportunity-search" className="sr-only">Search opportunities by asset</label>
            <InputGroup className="h-8 rounded-md border-transparent bg-transparent shadow-none hover:bg-white/[0.02] focus-within:border-white/[0.08] focus-within:bg-white/[0.035]">
              <InputGroupInput
                id="opportunity-search"
                type="search"
                value={query}
                onChange={(event) => onQueryChange(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Escape') onQueryChange('')
                }}
                placeholder="Search assets..."
                autoComplete="off"
                className="text-sm [&::-webkit-search-cancel-button]:appearance-none"
              />
              <InputGroupAddon align="inline-start">
                <SearchIcon />
              </InputGroupAddon>
              {query && (
                <InputGroupAddon align="inline-end">
                  <InputGroupButton
                    aria-label="Clear asset search"
                    onClick={() => onQueryChange('')}
                    className="cursor-pointer text-muted-foreground"
                  >
                    <XIcon />
                  </InputGroupButton>
                </InputGroupAddon>
              )}
            </InputGroup>
          </div>
          <div className="min-w-0 overflow-x-auto">
            <ToggleGroup
              multiple
              value={selectedVenues}
              onValueChange={(next) => {
                setSelectedVenues((current) => enforceMinimumVenueSelection(current, next))
              }}
              size="sm"
              spacing={1}
              aria-label="Filter opportunities by venues"
            >
              {FILTER_VENUES.map((venue) => {
                const metadata = venueMetadata(venue)
                return (
                  <ToggleGroupItem
                    key={venue}
                    value={venue}
                    disabled={selectedVenues.length === 2 && selectedVenues.includes(venue)}
                    aria-label={`${selectedVenues.includes(venue) ? 'Hide' : 'Show'} ${metadata.label} pairs`}
                    title={selectedVenues.length === 2 && selectedVenues.includes(venue)
                      ? 'At least two venues must stay selected'
                      : undefined}
                    className="cursor-pointer border border-transparent text-foreground hover:bg-white/[0.03] aria-pressed:border-white/[0.14] aria-pressed:bg-white/[0.07] disabled:cursor-not-allowed disabled:opacity-100"
                  >
                    {metadata.logo && <img src={metadata.logo} alt="" className="size-3.5 rounded-sm" />}
                    {metadata.label}
                  </ToggleGroupItem>
                )
              })}
            </ToggleGroup>
          </div>
        </div>
      </div>
      <div
        ref={tableViewportRef}
        className="flex-1 overflow-auto min-h-0 bg-[#080b12]"
        onPointerEnter={() => { pointerInside.current = true }}
        onPointerLeave={() => {
          pointerInside.current = false
          finishInteraction()
        }}
        onFocusCapture={() => { focusInside.current = true }}
        onBlurCapture={(event) => {
          if (event.currentTarget.contains(event.relatedTarget as Node | null)) return
          focusInside.current = false
          finishInteraction()
        }}
        onScroll={handleTableScroll}
      >
        {error && <p className="text-destructive text-sm px-5 py-6">Error: {error}</p>}
        {!loading && !error && opportunities.length === 0 && (
          <p className="text-muted-foreground text-sm px-5 py-6">No opportunities detected yet. Waiting for scan...</p>
        )}
        {!loading && !error && opportunities.length > 0 && displayed.length === 0 && (
          <p className="text-muted-foreground text-sm px-5 py-6">
            {query.trim()
              ? `No opportunities match “${query.trim()}” and the selected venues.`
              : 'No opportunities match the selected venues.'}
          </p>
        )}
        {!loading && displayed.length > 0 && (
          <Table className="min-w-[1080px]">
            <TableHeader className="sticky top-0 z-10 bg-[#0b0f17]/95 backdrop-blur-md">
              <TableRow className="border-b border-white/[0.08] bg-transparent shadow-[0_1px_0_rgba(255,255,255,0.02)] hover:bg-transparent">
                <SortTH field="asset" label="Asset" current={sortField} dir={sortDir} onSort={handleSort} />
                <TableHead className="h-9 text-left text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">Position</TableHead>
                <TableHead className="h-9 text-right text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">Venue Funding</TableHead>
                <SortTH field="fundingSpread" label="Spread" current={sortField} dir={sortDir} onSort={handleSort} right divided />
                <SortTH field="apr" label="Current APR" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SignalSortTH current={sortField} dir={sortDir} onSort={handleSort} />
                <SortTH field="priceSpread" label="Entry" current={sortField} dir={sortDir} onSort={handleSort} right divided />
                <SortTH field="capacity" label="Liquidity" current={sortField} dir={sortDir} onSort={handleSort} right />
                <TableHead className="h-9 w-8" />
              </TableRow>
            </TableHeader>
            <TableBody className="[&_tr:nth-child(even)]:bg-white/[0.008]">
              {displayed.map((opp) => {
                const isLongA = opp.direction === 'long_a_short_b'
                const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
                const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
                const venueA = venueMetadata(opp.venue_pair.venue_a)
                const venueB = venueMetadata(opp.venue_pair.venue_b)
                const maxLev = knownMaxLeverage(opp.max_leverage)
                const apr = opp.annualized_gross_edge

                return (
                  <TableRow
                    key={opp.id}
                    ref={(row) => {
                      if (row) rowRefs.current.set(opp.id, row)
                      else rowRefs.current.delete(opp.id)
                    }}
                    className={`group cursor-pointer border-b border-white/[0.045] outline-none transition-[background-color,box-shadow,opacity] hover:bg-white/[0.035] focus-visible:bg-white/[0.04] focus-visible:shadow-[inset_2px_0_0_#3b82f6] ${opp.status === 'available' ? '' : 'opacity-60'}`}
                    onClick={() => onSelect(opp.id)}
                  >
                    <TableCell className="py-3">
                      <div className="flex items-center gap-2.5">
                        <AssetIcon asset={opp.asset} />
                        <div>
                          <p className="font-semibold text-foreground">{opp.asset}</p>
                          <p className="mt-0.5 text-[10px] text-muted-foreground/80">
                            Up to {maxLev === null ? '--' : `${maxLev}x`} · <span className="capitalize">{opp.liquidity}</span> liquidity
                            {opp.status !== 'available' && <span className={opp.status === 'degraded' ? ' text-yellow-400' : ' text-red-400'}> · <span className="capitalize">{opp.status}</span></span>}
                          </p>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="py-3">
                      <PositionRoute longVenue={longVenue} shortVenue={shortVenue} />
                    </TableCell>
                    <TableCell className="py-3 text-right font-mono">
                      <FundingRateLine label={venueA.shortLabel} value={opp.funding_rate_a} color={venueA.textColor} />
                      <FundingRateLine label={venueB.shortLabel} value={opp.funding_rate_b} color={venueB.textColor} />
                    </TableCell>
                    <TableCell className="border-l border-white/[0.035] py-3 text-right font-mono text-foreground">
                      <MetricFlash value={Math.abs(opp.funding_spread)}>{fmtRate(Math.abs(opp.funding_spread))}</MetricFlash>
                      <p className="mt-0.5 text-[10px] font-sans text-muted-foreground">per hour</p>
                    </TableCell>
                    <TableCell className="py-3 text-right font-mono">
                      <p className="font-semibold text-emerald-400"><MetricFlash value={apr}>{fmtPct(apr)}</MetricFlash></p>
                      <p className="mt-0.5 text-[10px] text-muted-foreground">{maxLev === null ? '-- at --' : `${fmtPct(apr * maxLev)} at ${maxLev}x`}</p>
                    </TableCell>
                    <TableCell className="py-3 text-right">
                      <OpportunitySignalCell signal={opp.signal_7d} />
                    </TableCell>
                    <TableCell className={`border-l border-white/[0.035] py-3 text-right font-mono ${opp.entry_spread_estimate < 0 ? 'text-red-400' : 'text-foreground'}`}>
                      <MetricFlash value={opp.entry_spread_estimate}>{fmtPct(opp.entry_spread_estimate, 4)}</MetricFlash>
                      <p className="mt-0.5 text-[10px] font-sans text-muted-foreground">price spread</p>
                    </TableCell>
                    <TableCell className="py-3 text-right font-mono text-foreground">
                      <MetricFlash value={opp.best_price_capacity}>{fmtUsd(opp.best_price_capacity)}</MetricFlash>
                      <p className="mt-0.5 text-[10px] font-sans text-muted-foreground">OI {fmtUsd(opp.available_notional)}</p>
                    </TableCell>
                    <TableCell className="py-3">
                      <button
                        type="button"
                        aria-label={`Open ${opp.asset} opportunity details`}
                        className="inline-flex size-7 cursor-pointer items-center justify-center rounded text-muted-foreground opacity-35 outline-none transition-all hover:bg-white/[0.05] hover:opacity-80 focus-visible:bg-white/[0.06] focus-visible:opacity-100 focus-visible:ring-1 focus-visible:ring-blue-400/40 group-hover:translate-x-0.5 group-hover:opacity-80"
                        onClick={(event) => {
                          event.stopPropagation()
                          onSelect(opp.id)
                        }}
                      >
                        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                          <path d="M6 3l5 5-5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                        </svg>
                      </button>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </div>
      </>)}
    </>
  )
}

/* ── Opportunity Detail ────────────────────────────────── */

function OpportunityDetail({ opportunity: opp, notional, onNotionalChange, onBack }: {
  opportunity: Opportunity
  notional: number
  onNotionalChange: (value: number) => void
  onBack: () => void
}) {
  const isLongA = opp.direction === 'long_a_short_b'
  const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
  const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
  const maxLev = knownMaxLeverage(opp.max_leverage)

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="px-5 pt-4 pb-2 shrink-0">
        <div className="flex items-center gap-2">
          <button onClick={onBack} className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors -ml-1">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M10 3L5 8l5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>
          <AssetIcon asset={opp.asset} />
          <h2 className="text-xl font-bold text-foreground">{opp.asset}</h2>
        </div>
      </div>
      <div className="px-5 py-2.5 flex items-center gap-6 border-b border-border shrink-0 overflow-x-auto">
        <DetailStatItem label="Long"><DetailVenueIcon venue={longVenue} /></DetailStatItem>
        <DetailStatItem label="Short"><DetailVenueIcon venue={shortVenue} /></DetailStatItem>
        <DetailStatItem label="Max Leverage" value={maxLev === null ? '--' : `${maxLev}x`} />
        <DetailStatItem label="1h Spread" value={fmtRate(opp.funding_spread)} mono />
        <DetailStatItem label="APR" value={fmtPct(opp.annualized_gross_edge)} mono />
        <DetailStatItem label="APR x Max Lev" value={maxLev === null ? '--' : fmtPct(opp.annualized_gross_edge * maxLev)} mono />
        <DetailStatItem label="Price Spread" value={fmtPct(opp.entry_spread_estimate, 4)} mono negative={opp.entry_spread_estimate < 0} />
        <DetailStatItem label="Best Price Capacity" value={fmtUsd(opp.best_price_capacity)} mono />
        <DetailStatItem label="Open Interest" value={fmtUsd(opp.available_notional)} mono />
      </div>
      <div className="flex-1 overflow-auto min-h-0 px-5 py-4">
        <DeferredBoundary label="Funding history">
          <Suspense fallback={<DeferredSurface label="Loading funding history..." />}>
            <FundingChart
              asset={opp.asset}
              venueA={opp.venue_pair.venue_a}
              venueB={opp.venue_pair.venue_b}
              direction={opp.direction}
              currentApr={opp.annualized_gross_edge}
              recommendedNotional={opp.recommended_notional}
              notional={notional}
              onNotionalChange={onNotionalChange}
              feeEstimate={opp.fee_estimate}
              slippageEstimate={opp.slippage_estimate}
            />
          </Suspense>
        </DeferredBoundary>
      </div>
    </div>
  )
}

function PageBg({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex-1 overflow-auto min-h-0 bg-[#070a10] relative">
      <div className="pointer-events-none absolute inset-0 overflow-hidden">
        <div className="absolute -top-[200px] -right-[100px] w-[600px] h-[600px] rounded-full bg-blue-500/[0.03] blur-[120px]" />
        <div className="absolute top-[40%] -left-[150px] w-[500px] h-[500px] rounded-full bg-cyan-400/[0.025] blur-[100px]" />
        <div className="absolute -bottom-[100px] right-[20%] w-[400px] h-[400px] rounded-full bg-blue-600/[0.02] blur-[80px]" />
      </div>
      <div className="relative">{children}</div>
    </div>
  )
}

/* ── Shared ────────────────────────────────────────────── */

// Header button for opening Connect Accounts. Label is always "Accounts" —
// the dot color / border color carry the state (green ready, yellow needs
// attention, neutral not connected). Detailed reasons live inside the panel
// itself and in the hover tooltip; the header avoids duplicating them.
function AccountsHeaderButton({
  aggregate,
  open,
  onClick,
}: {
  aggregate: {
    tradingReady: boolean
    statusLabel: 'Ready' | 'Needs attention' | 'Not connected'
    blockingReasons: string[]
  }
  open: boolean
  onClick: () => void
}) {
  const notConnected = aggregate.statusLabel === 'Not connected'
  const ready = aggregate.tradingReady

  const tone = open
    ? 'border-blue-500/40 bg-blue-500/10 text-blue-400'
    : ready
      ? 'border-green-500/30 bg-green-500/[0.06] text-green-400 hover:bg-green-500/10'
      : notConnected
        ? 'border-border bg-white/[0.04] text-muted-foreground hover:text-foreground hover:bg-white/[0.08]'
        : 'border-yellow-500/30 bg-yellow-500/[0.06] text-yellow-400 hover:bg-yellow-500/10'

  const dot = ready ? 'bg-green-400' : notConnected ? 'bg-zinc-500' : 'bg-yellow-400'

  // Tooltip: state on the first line, reasons below when not ready.
  const title = ready
    ? 'Accounts ready'
    : notConnected
      ? 'No accounts connected'
      : aggregate.blockingReasons.length > 0
        ? `Needs attention\n${aggregate.blockingReasons.join('\n')}`
        : 'Needs attention'

  return (
    <button
      onClick={onClick}
      title={title}
      className={`flex items-center gap-1.5 rounded border px-2.5 py-1 text-[11px] font-medium transition-colors ${tone}`}
    >
      <div className={`size-1.5 rounded-full ${dot}`} />
      Accounts
    </button>
  )
}

function NavBtn({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`relative cursor-pointer rounded-md px-3 py-1.5 text-sm font-medium outline-none transition-[color,background-color,box-shadow] focus-visible:ring-1 focus-visible:ring-cyan-400/40 ${active ? 'nav-glass-active text-foreground' : 'text-muted-foreground hover:bg-white/[0.03] hover:text-foreground'}`}
    >
      {children}
    </button>
  )
}

function MarketingNavBtn({ children }: { children: React.ReactNode }) {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger render={(
          <button
            type="button"
            aria-disabled="true"
            className="relative cursor-help rounded-md px-3 py-1.5 text-sm font-medium text-muted-foreground outline-none transition-[color,background-color] hover:bg-white/[0.03] hover:text-foreground focus-visible:bg-white/[0.03] focus-visible:text-foreground focus-visible:ring-1 focus-visible:ring-cyan-400/40"
          >
            {children}
          </button>
        )} />
        <TooltipContent side="bottom" className="px-2 py-1 text-[10px]">
          Coming soon
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

function SortTH({ field, label, current, dir, onSort, right, divided }: {
  field: SortField
  label: string
  current: SortField
  dir: SortDir
  onSort: (f: SortField) => void
  right?: boolean
  divided?: boolean
}) {
  const active = current === field
  return (
    <TableHead
      aria-sort={active ? (dir === 'asc' ? 'ascending' : 'descending') : 'none'}
      className={`h-9 p-0 text-[10px] font-semibold uppercase tracking-[0.08em] ${divided ? 'border-l border-white/[0.035]' : ''} ${active ? 'text-foreground' : 'text-muted-foreground/80'}`}
      style={{ textAlign: right ? 'right' : 'left' }}
    >
      <button
        type="button"
        onClick={() => onSort(field)}
        className={`flex h-full w-full cursor-pointer items-center gap-1 px-4 uppercase outline-none transition-colors hover:text-foreground focus-visible:bg-white/[0.04] focus-visible:text-foreground ${right ? 'justify-end' : ''}`}
      >
        {label}
        <SortIndicator active={active} dir={dir} />
      </button>
    </TableHead>
  )
}

function SignalSortTH({ current, dir, onSort }: {
  current: SortField
  dir: SortDir
  onSort: (field: SortField) => void
}) {
  const active = current === 'signal7d'
  return (
    <TableHead
      aria-sort={active ? (dir === 'asc' ? 'ascending' : 'descending') : 'none'}
      className={`h-9 p-0 text-[10px] font-semibold uppercase tracking-[0.08em] ${active ? 'text-foreground' : 'text-muted-foreground/80'}`}
    >
      <div className="flex h-full items-center justify-end">
        <button
          type="button"
          onClick={() => onSort('signal7d')}
          className="flex h-full cursor-pointer items-center gap-1 pl-4 uppercase outline-none transition-colors hover:text-foreground focus-visible:bg-white/[0.04] focus-visible:text-foreground"
        >
          7d Signal
          <SortIndicator active={active} dir={dir} />
        </button>
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger render={<button type="button" aria-label="Explain 7-day signal" className="mr-4 inline-flex size-4 cursor-help items-center justify-center rounded text-muted-foreground/70 outline-none transition-colors hover:bg-white/[0.06] hover:text-foreground focus-visible:bg-white/[0.06] focus-visible:text-foreground"><InfoIcon className="size-3" /></button>} />
            <TooltipContent side="top" align="end" className="block max-w-64 space-y-1 text-left normal-case tracking-normal">
              <p>7-day carry quality from hourly funding.</p>
              <p><strong>Avg:</strong> average annualized spread.</p>
              <p><strong>Active:</strong> hours above 1% APR.</p>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>
    </TableHead>
  )
}

function SortIndicator({ active, dir }: { active: boolean; dir: SortDir }) {
  return active ? (
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="shrink-0">
      <path d={dir === 'desc' ? 'M2 4l3 3 3-3' : 'M2 6l3-3 3 3'} stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
    </svg>
  ) : (
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="shrink-0 opacity-30">
      <path d="M3 4l2-2 2 2M3 6l2 2 2-2" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round"/>
    </svg>
  )
}

function PositionRoute({ longVenue, shortVenue }: { longVenue: string; shortVenue: string }) {
  return (
    <div className="flex items-center gap-2">
      <div className="flex items-center gap-1.5">
        <DetailVenueIcon venue={longVenue} />
        <span className="text-[9px] font-medium uppercase tracking-wide text-emerald-400">Long</span>
      </div>
      <svg width="14" height="14" viewBox="0 0 14 14" fill="none" className="shrink-0 text-muted-foreground/40">
        <path d="M2 7h10M9 4l3 3-3 3" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
      <div className="flex items-center gap-1.5">
        <DetailVenueIcon venue={shortVenue} />
        <span className="text-[9px] font-medium uppercase tracking-wide text-rose-400">Short</span>
      </div>
    </div>
  )
}

function FundingRateLine({ label, value, color }: { label: string; value: number | null; color: string }) {
  return (
    <p className={value !== null && value < 0 ? 'text-red-400' : 'text-foreground'}>
      <span className={`mr-1.5 text-[9px] font-sans font-semibold ${color}`}>{label}</span>
      <MetricFlash value={value}>{value !== null ? fmtRate(value) : '—'}</MetricFlash>
    </p>
  )
}

const MetricFlash = memo(function MetricFlash({ value, children }: { value: number | null; children: React.ReactNode }) {
  const previousValue = useRef(value)
  const elementRef = useRef<HTMLSpanElement>(null)

  useEffect(() => {
    const previous = previousValue.current
    previousValue.current = value
    if (previous === null || value === null || previous === value) return
    const element = elementRef.current
    if (!element || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    element.getAnimations().forEach((animation) => animation.cancel())
    const increased = value > previous
    element.animate(
      [
        { color: increased ? '#6ee7b7' : '#fda4af' },
        { color: getComputedStyle(element).color },
      ],
      { duration: 750, easing: 'ease-out' },
    )
  }, [value])

  return <span ref={elementRef}>{children}</span>
})

const signalLabels: Record<OpportunitySignalStatus, string> = {
  persistent: 'Persistent',
  intermittent: 'Intermittent',
  new: 'New',
  choppy: 'Choppy',
  reversed: 'Reversed',
  faded: 'Faded',
  flat: 'Flat',
  limited: 'Limited',
}

const signalMoons: Record<OpportunitySignalStatus, string> = {
  persistent: '🌕',
  intermittent: '🌓',
  new: '🌒',
  choppy: '🌗',
  reversed: '🌘',
  faded: '🌘',
  flat: '🌑',
  limited: '🌑',
}

function OpportunitySignalCell({ signal }: { signal: OpportunitySignal | null }) {
  if (!signal) return <span className="font-mono text-muted-foreground">—</span>

  const activity = Math.round(signal.activity * 100)
  let detail = `${fmtPct(signal.average_edge)} avg · ${activity}% active`
  if (signal.status === 'new') detail = `${activity}% active · recent`
  if (signal.status === 'choppy') detail = `${activity}% active · flipping`
  if (signal.status === 'reversed') detail = `${activity}% active · reversed`
  if (signal.status === 'faded') detail = `${fmtPct(signal.average_edge)} avg · ${activity}% active`
  if (signal.status === 'flat') detail = 'no meaningful carry'
  if (signal.status === 'limited') detail = `${signal.samples} paired samples`
  const moon = signalMoons[signal.status]

  return (
    <div className="ml-auto grid w-40 grid-cols-[minmax(0,1fr)_22px] items-center gap-2 text-right">
      <span className="min-w-0">
        <span className="block text-[11px] font-semibold text-foreground">{signalLabels[signal.status]}</span>
        <span className="mt-0.5 block whitespace-nowrap text-[10px] text-muted-foreground">{detail}</span>
      </span>
      <span className={`text-[20px] leading-none ${signal.status === 'persistent' ? 'signal-moon-glow' : 'opacity-80'}`} aria-hidden="true">{moon}</span>
    </div>
  )
}
