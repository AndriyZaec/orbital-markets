import { useState, useMemo } from 'react'
import { useOpportunities } from '@/hooks/useOpportunities'
import { usePlan } from '@/hooks/usePlan'
import type { Opportunity } from '@/hooks/useOpportunities'
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { OpportunityPanel } from '@/components/OpportunityPanel'
import { PlanPreview } from '@/components/PlanPreview'
import { PaperPositions } from '@/components/PaperPositions'
import { AnalyticsDashboard } from '@/components/AnalyticsDashboard'
import { FundingChart } from '@/components/FundingChart'
import { getMockLeverage, getMockApr24h, getMockApr7d, getMockDailyVolume } from '@/lib/hacks'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'

type View = 'trade' | 'analytics'
type SortField = 'asset' | 'apr' | 'apr24h' | 'apr7d' | 'aprMaxLev' | 'priceSpread' | 'oi' | 'volume'
type SortDir = 'asc' | 'desc'

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
    case 'apr24h': return getMockApr24h(opp.annualized_gross_edge)
    case 'apr7d': return getMockApr7d(opp.annualized_gross_edge)
    case 'aprMaxLev': return opp.annualized_gross_edge * getMockLeverage(opp.asset)
    case 'priceSpread': return opp.entry_spread_estimate
    case 'oi': return opp.available_notional
    case 'volume': return getMockDailyVolume(opp.available_notional)
  }
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
  const [planOppId, setPlanOppId] = useState<string | null>(null)
  const [leverage, setLeverage] = useState(1)
  const { plan, loading: planLoading, error: planError, clear: clearPlan } = usePlan(planOppId, leverage)

  const selected = opportunities.find((o) => o.id === selectedId) ?? null

  const handleOpenSpread = (oppId: string) => {
    setPlanOppId(oppId)
    setLeverage(1)
  }

  const handleClosePlan = () => {
    setPlanOppId(null)
    clearPlan()
  }

  const handleExecutePaper = async (opportunityId: string) => {
    try {
      const resp = await fetch('/api/v1/paper/open', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ opportunity_id: opportunityId, leverage }),
      })
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}))
        alert(body.error || `Execution failed: HTTP ${resp.status}`)
        return
      }
      handleClosePlan()
      setSelectedId(null)
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Unknown error')
    }
  }

  return (
    <div className="dark h-screen bg-background flex flex-col overflow-hidden">
      <header className="h-12 border-b border-border flex items-center px-5 shrink-0">
        <div className="flex items-center gap-2.5 mr-10">
          <OrbitalLogo />
          <span className="text-[15px] font-semibold tracking-tight text-foreground">Orbital Market</span>
        </div>
        <nav className="flex items-center gap-1">
          <NavBtn active={activeView === 'trade'} onClick={() => setActiveView('trade')}>Trade</NavBtn>
          <NavBtn active={activeView === 'analytics'} onClick={() => setActiveView('analytics')}>Analytics</NavBtn>
        </nav>
        <div className="ml-auto flex items-center gap-1.5">
          <div className="size-1.5 rounded-full bg-yellow-400" />
          <span className="text-xs text-muted-foreground">Paper</span>
        </div>
      </header>

      {activeView === 'trade' && (
        <div className="flex-1 flex min-h-0">
          <div className="flex-1 flex flex-col min-w-0 min-h-0">
            <div className="flex-1 flex flex-col min-h-0">
              {selected ? (
                <OpportunityDetail opportunity={selected} onBack={() => setSelectedId(null)} />
              ) : (
                <OpportunityTable opportunities={opportunities} loading={loading} error={error} onSelect={setSelectedId} />
              )}
            </div>
            <div className="h-[280px] shrink-0 border-t border-border flex flex-col min-h-0">
              <PaperPositions />
            </div>
          </div>
          {selected && (
            <OpportunityPanel
              opportunity={selected}
              lastUpdated={lastUpdated}
              onClose={() => setSelectedId(null)}
              onOpenSpread={() => handleOpenSpread(selected.id)}
            />
          )}
        </div>
      )}

      {activeView === 'analytics' && (
        <div className="flex-1 overflow-auto min-h-0"><AnalyticsDashboard /></div>
      )}

      {plan && (
        <PlanPreview plan={plan} loading={planLoading} error={planError} leverage={leverage}
          onLeverageChange={setLeverage} onClose={handleClosePlan} onExecute={handleExecutePaper} />
      )}
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
      <div className="px-5 pt-5 pb-3 shrink-0">
        <h2 className="text-base font-bold text-foreground">Funding Rate Arb Opportunities</h2>
      </div>
      <div className="flex-1 overflow-auto min-h-0">
        {loading && <p className="text-muted-foreground text-sm px-5 py-6">Loading...</p>}
        {error && <p className="text-destructive text-sm px-5 py-6">Error: {error}</p>}
        {!loading && !error && opportunities.length === 0 && (
          <p className="text-muted-foreground text-sm px-5 py-6">No opportunities detected yet. Waiting for scan...</p>
        )}
        {sorted.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <SortTH field="asset" label="Asset" current={sortField} dir={sortDir} onSort={handleSort} />
                <th className="h-10 px-4 text-left align-middle font-normal whitespace-nowrap text-muted-foreground text-[13px]">Long</th>
                <th className="h-10 px-4 text-left align-middle font-normal whitespace-nowrap text-muted-foreground text-[13px]">Short</th>
                <SortTH field="apr" label="APR" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="apr24h" label="APR 24h" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="apr7d" label="APR 7d" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="aprMaxLev" label="APR x Max Leverage" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="priceSpread" label="Price Spread" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="oi" label="Open Interest" current={sortField} dir={sortDir} onSort={handleSort} right />
                <SortTH field="volume" label="Daily Volume" current={sortField} dir={sortDir} onSort={handleSort} right />
                <th className="w-8" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {sorted.map((opp) => {
                const isLongA = opp.direction === 'long_a_short_b'
                const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
                const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
                const maxLev = getMockLeverage(opp.asset)
                const apr = opp.annualized_gross_edge
                const apr24h = getMockApr24h(apr)
                const apr7d = getMockApr7d(apr)
                const dailyVol = getMockDailyVolume(opp.available_notional)

                return (
                  <TableRow key={opp.id} className="cursor-pointer" onClick={() => onSelect(opp.id)}>
                    <TableCell className="font-medium text-foreground">
                      {opp.asset}<span className="text-muted-foreground ml-1.5 text-xs">{maxLev}x</span>
                    </TableCell>
                    <TableCell><VenueIcon venue={longVenue} /></TableCell>
                    <TableCell><VenueIcon venue={shortVenue} /></TableCell>
                    <TableCell className="text-right font-mono text-foreground">{fmtPct(apr)}</TableCell>
                    <TableCell className={`text-right font-mono ${apr24h < 0 ? 'text-red-400' : 'text-foreground'}`}>{fmtPct(apr24h)}</TableCell>
                    <TableCell className={`text-right font-mono ${apr7d < 0 ? 'text-red-400' : 'text-foreground'}`}>{fmtPct(apr7d)}</TableCell>
                    <TableCell className="text-right font-mono text-foreground">{fmtPct(apr * maxLev)}</TableCell>
                    <TableCell className={`text-right font-mono ${opp.entry_spread_estimate < 0 ? 'text-red-400' : 'text-foreground'}`}>{fmtPct(opp.entry_spread_estimate, 4)}</TableCell>
                    <TableCell className="text-right font-mono text-foreground">{fmtUsd(opp.available_notional)}</TableCell>
                    <TableCell className="text-right font-mono text-foreground">{fmtUsd(dailyVol)}</TableCell>
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
    </>
  )
}

/* ── Opportunity Detail ────────────────────────────────── */

function OpportunityDetail({ opportunity: opp, onBack }: { opportunity: Opportunity; onBack: () => void }) {
  const isLongA = opp.direction === 'long_a_short_b'
  const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
  const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a
  const maxLev = getMockLeverage(opp.asset)

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
        <StatItem label="APR 24h" value={fmtPct(getMockApr24h(opp.annualized_gross_edge))} mono />
        <StatItem label="APR 7d" value={fmtPct(getMockApr7d(opp.annualized_gross_edge))} mono />
        <StatItem label="APR x Max Lev" value={fmtPct(opp.annualized_gross_edge * maxLev)} mono />
        <StatItem label="Price Spread" value={fmtPct(opp.entry_spread_estimate, 4)} mono negative={opp.entry_spread_estimate < 0} />
        <StatItem label="Open Interest" value={fmtUsd(opp.available_notional)} mono />
        <StatItem label="Daily Volume" value={fmtUsd(getMockDailyVolume(opp.available_notional))} mono />
      </div>
      <div className="flex-1 overflow-auto min-h-0 px-5 py-4">
        <FundingChart asset={opp.asset} currentSpread={opp.funding_spread} />
      </div>
    </div>
  )
}

/* ── Shared ────────────────────────────────────────────── */

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
    <th
      className={`h-10 px-4 align-middle font-normal whitespace-nowrap text-[13px] cursor-pointer select-none transition-colors hover:text-foreground ${right ? 'text-right' : 'text-left'} ${active ? 'text-foreground' : 'text-muted-foreground'}`}
      onClick={() => onSort(field)}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        {active && (
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="shrink-0">
            <path d={dir === 'desc' ? 'M2 4l3 3 3-3' : 'M2 6l3-3 3 3'} stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        )}
        {!active && (
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" className="shrink-0 opacity-30">
            <path d="M3 4l2-2 2 2M3 6l2 2 2-2" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round"/>
          </svg>
        )}
      </span>
    </th>
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
