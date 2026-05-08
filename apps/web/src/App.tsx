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

type View = 'opportunities' | 'positions' | 'analytics'

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

function directionIcons(opp: Opportunity) {
  const isLongA = opp.direction === 'long_a_short_b'
  return (
    <div className="flex items-center gap-1.5">
      <span className={`text-xs font-semibold ${isLongA ? 'text-green-400' : 'text-red-400'}`}>
        {isLongA ? 'L' : 'S'}
      </span>
      <span className="text-muted-foreground text-[10px]">/</span>
      <span className={`text-xs font-semibold ${isLongA ? 'text-red-400' : 'text-green-400'}`}>
        {isLongA ? 'S' : 'L'}
      </span>
    </div>
  )
}

export default function App() {
  const [activeView, setActiveView] = useState<View>('opportunities')
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
      setActiveView('positions')
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Unknown error')
    }
  }

  const navItems: { key: View; label: string }[] = [
    { key: 'opportunities', label: 'Trade' },
    { key: 'positions', label: 'Positions' },
    { key: 'analytics', label: 'Analytics' },
  ]

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
          {navItems.map((item) => (
            <button
              key={item.key}
              onClick={() => setActiveView(item.key)}
              className={`px-3.5 py-1.5 rounded-md text-sm font-medium transition-colors ${
                activeView === item.key
                  ? 'text-foreground bg-white/[0.06]'
                  : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {item.label}
            </button>
          ))}
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
        {/* Content Area */}
        <div className="flex-1 flex flex-col min-w-0">
          {activeView === 'opportunities' && (
            <>
              <div className="px-5 pt-5 pb-3 flex items-center justify-between">
                <h2 className="text-base font-semibold text-foreground">
                  Funding Rate Arb Opportunities
                </h2>
                {opportunities.length > 0 && (
                  <span className="text-xs text-muted-foreground">
                    {opportunities.length} opportunities
                  </span>
                )}
              </div>

              <div className="flex-1 overflow-auto">
                {loading && (
                  <p className="text-muted-foreground text-sm px-5 py-8">Loading...</p>
                )}
                {error && (
                  <p className="text-destructive text-sm px-5 py-8">Error: {error}</p>
                )}
                {!loading && !error && opportunities.length === 0 && (
                  <p className="text-muted-foreground text-sm px-5 py-8">
                    No opportunities detected yet. Waiting for scan...
                  </p>
                )}
                {opportunities.length > 0 && (
                  <Table>
                    <TableHeader>
                      <TableRow className="border-border hover:bg-transparent">
                        <TH>Asset</TH>
                        <TH></TH>
                        <TH>Long</TH>
                        <TH>Short</TH>
                        <TH right>APR</TH>
                        <TH right>Spread (h)</TH>
                        <TH right>Net Edge</TH>
                        <TH right>Notional</TH>
                        <TableHead className="w-8"></TableHead>
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
                            className={`cursor-pointer transition-colors border-border ${
                              selectedId === opp.id
                                ? 'bg-white/[0.04] border-l-2 border-l-blue-500'
                                : 'hover:bg-white/[0.02]'
                            }`}
                            onClick={() => setSelectedId(selectedId === opp.id ? null : opp.id)}
                          >
                            <TableCell className="font-medium text-foreground">
                              {opp.asset}
                            </TableCell>
                            <TableCell>
                              {directionIcons(opp)}
                            </TableCell>
                            <TableCell className="text-sm capitalize">
                              <span className="text-green-400/80">{longVenue}</span>
                            </TableCell>
                            <TableCell className="text-sm capitalize">
                              <span className="text-red-400/80">{shortVenue}</span>
                            </TableCell>
                            <TableCell className="text-right font-mono text-sm text-foreground">
                              {fmtPct(opp.annualized_gross_edge)}
                            </TableCell>
                            <TableCell className="text-right font-mono text-sm text-foreground">
                              {fmtRate(opp.funding_spread)}
                            </TableCell>
                            <TableCell className="text-right font-mono text-sm text-foreground">
                              {fmtPct(opp.estimated_net_edge)}
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
                )}
              </div>
            </>
          )}

          {activeView === 'positions' && <PaperPositions />}
          {activeView === 'analytics' && (
            <div className="flex-1 overflow-auto">
              <AnalyticsDashboard />
            </div>
          )}
        </div>

        {/* Detail sidebar */}
        {activeView === 'opportunities' && selected && (
          <OpportunityPanel
            opportunity={selected}
            lastUpdated={lastUpdated}
            onClose={() => setSelectedId(null)}
            onOpenSpread={() => handleOpenSpread(selected.id)}
          />
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

function TH({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <TableHead className={`text-muted-foreground font-medium text-xs uppercase tracking-wider ${right ? 'text-right' : ''}`}>
      {children}
    </TableHead>
  )
}
