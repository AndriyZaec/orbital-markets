import { useState } from 'react'
import { useOpportunities } from '@/hooks/useOpportunities'
import { usePlan } from '@/hooks/usePlan'
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
import { PlanPreview } from '@/components/PlanPreview'
import { PaperPositions } from '@/components/PaperPositions'
import { AnalyticsDashboard } from '@/components/AnalyticsDashboard'
import { FundingChart } from '@/components/FundingChart'

type View = 'trade' | 'analytics'

function fmtPct(n: number, decimals = 2) {
  return (n * 100).toFixed(decimals) + '%'
}

function fmtRate(n: number) {
  return (n * 100).toFixed(4) + '%'
}

function fmtUsd(n: number) {
  if (n >= 1_000_000_000) return '$' + (n / 1_000_000_000).toFixed(2) + 'b'
  if (n >= 1_000_000) return '$' + (n / 1_000_000).toFixed(2) + 'm'
  if (n >= 1_000) return '$' + (n / 1_000).toFixed(1) + 'k'
  return '$' + n.toFixed(2)
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
    <div className="dark min-h-screen bg-background flex flex-col">
      {/* Header Nav */}
      <header className="h-14 border-b border-border flex items-center px-5 shrink-0">
        <div className="flex items-center gap-2.5 mr-10">
          <OrbitalLogo />
          <span className="text-[15px] font-semibold tracking-tight text-foreground">
            Orbital Market
          </span>
        </div>

        <nav className="flex items-center gap-1">
          <NavBtn active={activeView === 'trade'} onClick={() => setActiveView('trade')}>Trade</NavBtn>
          <NavBtn active={activeView === 'analytics'} onClick={() => setActiveView('analytics')}>Analytics</NavBtn>
        </nav>

        <div className="ml-auto flex items-center gap-3">
          <div className="flex items-center gap-1.5">
            <div className="size-1.5 rounded-full bg-yellow-400" />
            <span className="text-xs text-muted-foreground">Paper</span>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <div className="flex-1 flex min-h-0">
        {activeView === 'trade' && (
          <>
            {/* Left: table or detail + positions */}
            <div className="flex-1 flex flex-col min-w-0 overflow-auto">
              {selected ? (
                <OpportunityDetail
                  opportunity={selected}
                  onBack={() => setSelectedId(null)}
                />
              ) : (
                <OpportunityTable
                  opportunities={opportunities}
                  loading={loading}
                  error={error}
                  onSelect={setSelectedId}
                />
              )}

              {/* Positions — always visible below */}
              <div className="border-t border-border">
                <PaperPositions />
              </div>
            </div>

            {/* Right sidebar: trade execution panel */}
            {selected && (
              <OpportunityPanel
                opportunity={selected}
                lastUpdated={lastUpdated}
                onClose={() => setSelectedId(null)}
                onOpenSpread={() => handleOpenSpread(selected.id)}
              />
            )}
          </>
        )}

        {activeView === 'analytics' && (
          <div className="flex-1 overflow-auto">
            <AnalyticsDashboard />
          </div>
        )}
      </div>

      {/* Plan preview modal */}
      {plan && (
        <PlanPreview
          plan={plan}
          loading={planLoading}
          error={planError}
          leverage={leverage}
          onLeverageChange={setLeverage}
          onClose={handleClosePlan}
          onExecute={handleExecutePaper}
        />
      )}
    </div>
  )
}

/* ── Opportunity Table ─────────────────────────────────── */

function OpportunityTable({ opportunities, loading, error, onSelect }: {
  opportunities: Opportunity[]
  loading: boolean
  error: string | null
  onSelect: (id: string) => void
}) {
  return (
    <div className="flex-1 flex flex-col min-h-0">
      <div className="px-5 pt-5 pb-3">
        <h2 className="text-base font-semibold text-foreground">
          Funding Rate Arb Opportunities
        </h2>
      </div>

      <div className="flex-1 overflow-auto">
        {loading && <p className="text-muted-foreground text-sm px-5 py-8">Loading...</p>}
        {error && <p className="text-destructive text-sm px-5 py-8">Error: {error}</p>}
        {!loading && !error && opportunities.length === 0 && (
          <p className="text-muted-foreground text-sm px-5 py-8">
            No opportunities detected yet. Waiting for scan...
          </p>
        )}
        {opportunities.length > 0 && (
          <>
            <Table>
              <TableHeader>
                <TableRow className="border-border hover:bg-transparent">
                  <TH>Asset</TH>
                  <TH>Long</TH>
                  <TH>Short</TH>
                  <TH right>APR</TH>
                  <TH right>1h Spread</TH>
                  <TH right>Price Spread</TH>
                  <TH right>Open Interest</TH>
                  <TableHead className="w-8" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {opportunities.map((opp) => {
                  const isLongA = opp.direction === 'long_a_short_b'
                  const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
                  const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a

                  return (
                    <TableRow
                      key={opp.id}
                      className="cursor-pointer transition-colors border-border hover:bg-white/[0.02]"
                      onClick={() => onSelect(opp.id)}
                    >
                      <TableCell className="font-medium text-foreground">{opp.asset}</TableCell>
                      <TableCell>
                        <VenueIcon venue={longVenue} side="long" />
                      </TableCell>
                      <TableCell>
                        <VenueIcon venue={shortVenue} side="short" />
                      </TableCell>
                      <TableCell className="text-right font-mono text-sm text-foreground">
                        {fmtPct(opp.annualized_gross_edge)}
                      </TableCell>
                      <TableCell className="text-right font-mono text-sm text-foreground">
                        {fmtRate(opp.funding_spread)}
                      </TableCell>
                      <TableCell className={`text-right font-mono text-sm ${opp.entry_spread_estimate < 0 ? 'text-red-400' : 'text-foreground'}`}>
                        {fmtPct(opp.entry_spread_estimate, 4)}
                      </TableCell>
                      <TableCell className="text-right font-mono text-sm text-muted-foreground">
                        {fmtUsd(opp.available_notional)}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" className="opacity-40">
                          <path d="M6 3l5 5-5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                        </svg>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
            <div className="px-5 py-3 text-xs text-muted-foreground">
              {opportunities.length} opportunities
            </div>
          </>
        )}
      </div>
    </div>
  )
}

/* ── Opportunity Detail (replaces table) ───────────────── */

function OpportunityDetail({ opportunity: opp, onBack }: {
  opportunity: Opportunity
  onBack: () => void
}) {
  const isLongA = opp.direction === 'long_a_short_b'
  const longVenue = isLongA ? opp.venue_pair.venue_a : opp.venue_pair.venue_b
  const shortVenue = isLongA ? opp.venue_pair.venue_b : opp.venue_pair.venue_a

  return (
    <div className="flex flex-col">
      {/* Title bar */}
      <div className="px-5 pt-5 pb-2">
        <p className="text-xs text-muted-foreground mb-2">Funding Rate Arb Opportunities</p>
        <div className="flex items-center gap-2">
          <button
            onClick={onBack}
            className="text-muted-foreground hover:text-foreground size-7 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors -ml-1"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M10 3L5 8l5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          </button>
          <h2 className="text-xl font-bold text-foreground">{opp.asset}</h2>
        </div>
      </div>

      {/* Stats bar */}
      <div className="px-5 py-3 flex items-center gap-6 border-b border-border overflow-x-auto">
        <StatItem label="Long">
          <VenueIcon venue={longVenue} side="long" />
        </StatItem>
        <StatItem label="Short">
          <VenueIcon venue={shortVenue} side="short" />
        </StatItem>
        <StatItem label="1h Spread" value={fmtRate(opp.funding_spread)} mono />
        <StatItem label="APR" value={fmtPct(opp.annualized_gross_edge)} mono />
        <StatItem label="Price Spread" value={fmtPct(opp.entry_spread_estimate, 4)} mono negative={opp.entry_spread_estimate < 0} />
        <StatItem label="Open Interest" value={fmtUsd(opp.available_notional)} mono />
      </div>

      {/* Historical Chart */}
      <div className="px-5 py-5">
        <FundingChart asset={opp.asset} currentSpread={opp.funding_spread} />
      </div>
    </div>
  )
}

/* ── Shared small components ───────────────────────────── */

function NavBtn({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`px-3.5 py-1.5 rounded-md text-sm font-medium transition-colors ${
        active ? 'text-foreground bg-white/[0.06]' : 'text-muted-foreground hover:text-foreground'
      }`}
    >
      {children}
    </button>
  )
}

function TH({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <TableHead className={`text-muted-foreground font-medium text-xs uppercase tracking-wider ${right ? 'text-right' : ''}`}>
      {children}
    </TableHead>
  )
}

function VenueIcon({ venue, side }: { venue: string; side: 'long' | 'short' }) {
  const color = side === 'long' ? 'text-green-400 bg-green-400/10 border-green-400/20' : 'text-red-400 bg-red-400/10 border-red-400/20'
  return (
    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded border text-xs font-medium capitalize ${color}`}>
      {venue}
    </span>
  )
}

function StatItem({ label, value, mono, negative, children }: {
  label: string
  value?: string
  mono?: boolean
  negative?: boolean
  children?: React.ReactNode
}) {
  return (
    <div className="shrink-0">
      <p className="text-[10px] text-muted-foreground mb-0.5">{label}</p>
      {children ?? (
        <p className={`text-sm font-medium ${mono ? 'font-mono' : ''} ${negative ? 'text-red-400' : 'text-foreground'}`}>
          {value}
        </p>
      )}
    </div>
  )
}
