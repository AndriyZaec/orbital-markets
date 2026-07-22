import { useState, useMemo, useEffect, useCallback, useRef } from 'react'
import { apiFetch } from '@/lib/api'
import { useOpportunities } from '@/hooks/useOpportunities'

import type { Opportunity } from '@/hooks/useOpportunities'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { OpportunityPanel } from '@/components/OpportunityPanel'

import { PaperPositions } from '@/components/PaperPositions'
import { LivePositions } from '@/components/LivePositions'
import { Portfolio } from '@/components/Portfolio'
import { useVenueReadiness } from '@/hooks/useVenueReadiness'
import { FeeRebates } from '@/components/FeeRebates'
import { ConnectAccounts } from '@/components/ConnectAccounts'
import { ForAgents } from '@/components/ForAgents'
import { FundingChart } from '@/components/FundingChart'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'

type View = 'trade' | 'portfolio' | 'rebates' | 'agents'
type SortField = 'asset' | 'apr' | 'aprMaxLev' | 'priceSpread' | 'oi' | 'fundingSpread' | 'pacificaRate' | 'hlRate'
type SortDir = 'asc' | 'desc'

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
    case 'aprMaxLev': return opp.annualized_gross_edge * (opp.max_leverage || 1)
    case 'priceSpread': return opp.entry_spread_estimate
    case 'oi': return opp.available_notional
    case 'fundingSpread': return Math.abs(opp.funding_spread)
    case 'pacificaRate': return fundingForVenue(opp, 'pacifica') ?? 0
    case 'hlRate': return fundingForVenue(opp, 'hyperliquid') ?? 0
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

function OrbitalLogo() {
  return (
    <div className="relative size-8 flex items-center justify-center">
      <div className="absolute inset-0 rounded-full border-2 border-slate-500/40" />
      <div className="absolute inset-1.5 rounded-full border-[1.5px] border-slate-400/50" />
      <div className="absolute size-2 rounded-full bg-cyan-400 shadow-[0_0_8px_rgba(6,182,212,0.6)]" />
    </div>
  )
}

export default function App() {
  const [activeView, setActiveView] = useState<View>('trade')
  const { opportunities, loading, error, lastUpdated } = useOpportunities()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [showAccounts, setShowAccounts] = useState(false)
  // Header account status is driven by the same typed readiness layer used
  // by Connect Accounts and Execute Live — one source of truth for the
  // "is this trader actually ready to trade" signal.
  const { aggregate: accountsAggregate } = useVenueReadiness()
  const [tradingMode, setTradingMode] = useState<'paper' | 'live'>('live')
  // Matches useOpportunities' 60s poll interval — scanner refreshes every 60s.
  const countdown = useCountdown(lastUpdated, 60)
  const isLive = countdown > 0

  const selected = opportunities.find((o) => o.id === selectedId) ?? null

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
        alert(body.error || `Execution failed: HTTP ${resp.status}`)
        return
      }
      setSelectedId(null)
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Unknown error')
    }
  }

  return (
    <div className="dark h-screen bg-background flex flex-col overflow-hidden">
      <header className="h-12 border-b border-border flex items-center px-5 shrink-0">
        <button className="flex items-center gap-2.5 mr-10 cursor-pointer" onClick={() => { setSelectedId(null); setActiveView('trade') }}>
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
          <NavBtn active={activeView === 'rebates'} onClick={() => setActiveView('rebates')}>Fee Rebates</NavBtn>
          <button
            onClick={() => setActiveView('agents')}
            className={`px-3.5 py-1.5 rounded-md text-sm font-medium transition-all ${
              activeView === 'agents' ? 'bg-white/[0.06]' : 'hover:bg-white/[0.03]'
            }`}
          >
            <span className="bg-clip-text text-transparent bg-gradient-to-r from-[#9945FF] via-[#14F195] to-[#9945FF] bg-[length:200%_100%] animate-[gradient-shift_6s_ease-in-out_infinite]">
              For Agents
            </span>
          </button>
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
                {selected ? (
                  <OpportunityDetail opportunity={selected} onBack={() => setSelectedId(null)} />
                ) : (
                  <OpportunityTable opportunities={opportunities} loading={loading} error={error} onSelect={setSelectedId} />
                )}
              </div>
              <div className="shrink-0 border-t border-border flex flex-col min-h-0 relative bg-[#080b12]" style={{ height: posHeight }}>
                <div
                  className="absolute top-0 left-0 right-0 h-1.5 cursor-row-resize z-10 hover:bg-blue-500/20 transition-colors"
                  onMouseDown={onResizeStart}
                />
                {tradingMode === 'paper' ? <PaperPositions /> : <LivePositions onConnectWallets={() => setShowAccounts(true)} />}
                <button
                  onClick={() => setTradingMode(tradingMode === 'live' ? 'paper' : 'live')}
                  className="absolute bottom-1.5 right-3 text-[10px] text-muted-foreground/40 hover:text-muted-foreground transition-colors"
                >
                  {tradingMode === 'live' ? 'paper mode' : 'live mode'}
                </button>
              </div>
            </>
          )}

          {activeView === 'portfolio' && (
            <PageBg>
              <Portfolio
                onConnectWallets={() => setShowAccounts(true)}
                onViewPositions={() => setActiveView('trade')}
              />
            </PageBg>
          )}

          {activeView === 'rebates' && (
            <PageBg><FeeRebates /></PageBg>
          )}

          {activeView === 'agents' && (
            <PageBg><ForAgents /></PageBg>
          )}
        </div>

        {activeView === 'trade' && selected && (
          <OpportunityPanel
            opportunity={selected}
            lastUpdated={lastUpdated}
            mode={tradingMode}
            onClose={() => setSelectedId(null)}
            onExecute={handleExecutePaper}
            onViewPositions={() => setSelectedId(null)}
            onOpenAccounts={() => setShowAccounts(true)}
          />
        )}

        <ConnectAccounts open={showAccounts} onClose={() => setShowAccounts(false)} />
      </div>

    </div>
  )
}

/* ── Opportunity Table ─────────────────────────────────── */

function OpportunityTable({ opportunities, loading, error, onSelect }: {
  opportunities: Opportunity[]; loading: boolean; error: string | null; onSelect: (id: string) => void
}) {
  const [sortField, setSortField] = useState<SortField>('apr')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortDir(sortDir === 'desc' ? 'asc' : 'desc')
    } else {
      setSortField(field)
      setSortDir('desc')
    }
  }

  const sorted = useMemo(() => {
    if (opportunities.length === 0) return opportunities
    return [...opportunities].sort((a, b) => {
      const va = getSortValue(a, sortField)
      const vb = getSortValue(b, sortField)
      const cmp = typeof va === 'string' ? va.localeCompare(vb as string) : (va as number) - (vb as number)
      return sortDir === 'desc' ? -cmp : cmp
    })
  }, [opportunities, sortField, sortDir])

  return (
    <>
      {loading ? (
        <div className="flex-1 flex flex-col items-center justify-center bg-[#080b12]">
          <div className="relative size-10 animate-[loader-pulse_2s_ease-in-out_infinite]">
            <div className="absolute inset-0 rounded-full border-2 border-slate-500/40" />
            <div className="absolute inset-1.5 rounded-full border-[1.5px] border-slate-400/50" />
            <div className="absolute inset-0 flex items-center justify-center">
              <div className="size-2 rounded-full bg-cyan-400 shadow-[0_0_8px_rgba(6,182,212,0.6)]" />
            </div>
          </div>
          <p className="text-muted-foreground text-xs mt-3">Scanning opportunities...</p>
        </div>
      ) : (<>
      <div className="px-5 pt-5 pb-3 shrink-0 bg-[#080b12]">
        <h2 className="text-base font-bold text-foreground">Funding Rate Arb Opportunities</h2>
      </div>
      <div className="flex-1 overflow-auto min-h-0 bg-[#080b12]">
        {error && <p className="text-destructive text-sm px-5 py-6">Error: {error}</p>}
        {!loading && !error && opportunities.length === 0 && (
          <p className="text-muted-foreground text-sm px-5 py-6">No opportunities detected yet. Waiting for scan...</p>
        )}
        {!loading && sorted.length > 0 && (
          <Table>
            <TableHeader className="sticky top-0 z-10">
              <TableRow className="hover:bg-transparent bg-[#080b12]">
                <SortTH field="asset" label="Asset" current={sortField} dir={sortDir} onSort={handleSort} />
                <TableHead className="text-left">Long</TableHead>
                <TableHead className="text-left">Short</TableHead>
                <SortTH field="pacificaRate" label="Pacifica Funding (1p)" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="hlRate" label="HL Funding (1p)" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="fundingSpread" label="Funding Spread (1p)" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="apr" label="APR" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="aprMaxLev" label="APR x Max Lev" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="priceSpread" label="Price Spread" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="oi" label="Open Interest" current={sortField} dir={sortDir} onSort={handleSort} right />
                <TableHead className="w-8" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {sorted.map((opp) => {
                const isLongA = opp.direction === 'long_a_short_b'
                const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
                const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
                const maxLev = opp.max_leverage || 1
                const apr = opp.annualized_gross_edge

                return (
                  <TableRow key={opp.id} className="cursor-pointer" onClick={() => onSelect(opp.id)}>
                    <TableCell className="font-medium text-foreground">
                      {opp.asset}<span className="text-muted-foreground ml-1.5 text-xs">{maxLev}x</span>
                    </TableCell>
                    <TableCell><VenueIcon venue={longVenue} /></TableCell>
                    <TableCell><VenueIcon venue={shortVenue} /></TableCell>
                    <TC negative={(fundingForVenue(opp, 'pacifica') ?? 0) < 0}>
                      {fundingForVenue(opp, 'pacifica') !== null ? fmtRate(fundingForVenue(opp, 'pacifica')!) : '—'}
                    </TC>
                    <TC negative={(fundingForVenue(opp, 'hyperliquid') ?? 0) < 0}>
                      {fundingForVenue(opp, 'hyperliquid') !== null ? fmtRate(fundingForVenue(opp, 'hyperliquid')!) : '—'}
                    </TC>
                    <TC>{fmtRate(Math.abs(opp.funding_spread))}</TC>
                    <TC>{fmtPct(apr)}</TC>
                    <TC>{fmtPct(apr * maxLev)}</TC>
                    <TC negative={opp.entry_spread_estimate < 0}>{fmtPct(opp.entry_spread_estimate, 4)}</TC>
                    <TC>{fmtUsd(opp.available_notional)}</TC>
                    <TableCell>
                      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" className="text-muted-foreground opacity-50">
                        <path d="M6 3l5 5-5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                      </svg>
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

function OpportunityDetail({ opportunity: opp, onBack }: { opportunity: Opportunity; onBack: () => void }) {
  const isLongA = opp.direction === 'long_a_short_b'
  const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
  const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
  const maxLev = opp.max_leverage || 1

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="px-5 pt-4 pb-2 shrink-0">
        <p className="text-[11px] text-muted-foreground mb-1">Funding Rate Arb Opportunities</p>
        <div className="flex items-center gap-2">
          <button onClick={onBack} className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors -ml-1">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M10 3L5 8l5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>
          <h2 className="text-xl font-bold text-foreground">{opp.asset}</h2>
        </div>
      </div>
      <div className="px-5 py-2.5 flex items-center gap-6 border-b border-border shrink-0 overflow-x-auto">
        <StatItem label="Long"><VenueIcon venue={longVenue} /></StatItem>
        <StatItem label="Short"><VenueIcon venue={shortVenue} /></StatItem>
        <StatItem label="Max Leverage" value={`${maxLev}x`} />
        <StatItem label="1h Spread" value={fmtRate(opp.funding_spread)} mono />
        <StatItem label="APR" value={fmtPct(opp.annualized_gross_edge)} mono />
        <StatItem label="APR x Max Lev" value={fmtPct(opp.annualized_gross_edge * maxLev)} mono />
        <StatItem label="Price Spread" value={fmtPct(opp.entry_spread_estimate, 4)} mono negative={opp.entry_spread_estimate < 0} />
        <StatItem label="Open Interest" value={fmtUsd(opp.available_notional)} mono />
      </div>
      <div className="flex-1 overflow-auto min-h-0 px-5 py-4">
        <FundingChart asset={opp.asset} venueA={opp.venue_pair.venue_a} venueB={opp.venue_pair.venue_b} />
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
    allReady: boolean
    statusLabel: 'Ready' | 'Needs attention' | 'Not connected'
    blockingReasons: string[]
  }
  open: boolean
  onClick: () => void
}) {
  const notConnected = aggregate.statusLabel === 'Not connected'
  const ready = aggregate.allReady

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
    <button onClick={onClick} className={`px-3.5 py-1.5 rounded-md text-sm font-medium transition-colors ${active ? 'text-foreground bg-white/[0.06]' : 'text-muted-foreground hover:text-foreground'}`}>
      {children}
    </button>
  )
}

function SortTH({ field, label, current, dir, onSort, right }: {
  field: SortField; label: string; current: SortField; dir: SortDir; onSort: (f: SortField) => void; right?: boolean
}) {
  const active = current === field
  return (
    <TableHead
      className={`cursor-pointer select-none transition-colors hover:text-foreground ${active ? 'text-foreground' : ''}`}
      style={{ textAlign: right ? 'right' : 'left' }}
      onClick={() => onSort(field)}
    >
      <span className={`flex items-center gap-1 ${right ? 'justify-end' : ''}`}>
        {label}
        {active ? (
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="shrink-0">
            <path d={dir === 'desc' ? 'M2 4l3 3 3-3' : 'M2 6l3-3 3 3'} stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        ) : (
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="shrink-0 opacity-30">
            <path d="M3 4l2-2 2 2M3 6l2 2 2-2" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        )}
      </span>
    </TableHead>
  )
}

function TC({ children, negative }: { children: React.ReactNode; negative?: boolean }) {
  return (
    <TableCell className={`font-mono ${negative ? 'text-red-400' : 'text-foreground'}`} style={{ textAlign: 'right' }}>
      {children}
    </TableCell>
  )
}

function VenueIcon({ venue }: { venue: string }) {
  const v = venue.toLowerCase()
  if (v === 'pacifica') {
    return <span className="inline-flex items-center justify-center size-7 rounded bg-white/[0.04] border border-border" title="Pacifica"><img src={pacificaLogo} alt="Pacifica" className="size-5" /></span>
  }
  if (v === 'hyperliquid') {
    return <span className="inline-flex items-center justify-center size-7 rounded bg-white/[0.04] border border-border" title="Hyperliquid"><img src={hlLogo} alt="Hyperliquid" className="size-5" /></span>
  }
  return <span className="inline-flex items-center justify-center size-7 rounded bg-white/[0.06] border border-border text-[11px] font-bold text-muted-foreground uppercase" title={venue}>{venue[0]}</span>
}

function StatItem({ label, value, mono, negative, children }: {
  label: string; value?: string; mono?: boolean; negative?: boolean; children?: React.ReactNode
}) {
  return (
    <div className="shrink-0">
      <p className="text-[10px] text-muted-foreground mb-0.5">{label}</p>
      {children ?? <p className={`text-sm font-medium ${mono ? 'font-mono' : ''} ${negative ? 'text-red-400' : 'text-foreground'}`}>{value}</p>}
    </div>
  )
}
