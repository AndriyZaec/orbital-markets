import { useState } from 'react'
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
import { Badge } from '@/components/ui/badge'
import { OpportunityPanel } from '@/components/OpportunityPanel'

function confidenceVariant(c: Opportunity['confidence']) {
  switch (c) {
    case 'high': return 'default' as const
    case 'medium': return 'secondary' as const
    case 'low': return 'outline' as const
  }
}

function riskColor(r: Opportunity['risk_tier']) {
  switch (r) {
    case 'conservative': return 'text-green-400'
    case 'standard': return 'text-blue-400'
    case 'aggressive': return 'text-yellow-400'
    case 'experimental': return 'text-red-400'
  }
}

function fmtPct(n: number, decimals = 2) {
  return (n * 100).toFixed(decimals) + '%'
}

function fmtRate(n: number) {
  return (n * 100).toFixed(4) + '%'
}

export default function App() {
  const { opportunities, loading, error, lastUpdated } = useOpportunities()
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const selected = opportunities.find((o) => o.id === selectedId) ?? null

  return (
    <div className="dark min-h-screen bg-background flex">
      {/* Main content */}
      <div className="flex-1 flex flex-col min-w-0">
        <header className="border-b border-border px-6 py-4">
          <h1 className="text-xl font-semibold tracking-tight">Orbital Market</h1>
          <p className="text-sm text-muted-foreground">
            Perp spread opportunities
            {opportunities.length > 0 && ` · ${opportunities.length} found`}
          </p>
        </header>

        <main className="p-6 flex-1 overflow-auto">
          {loading && <p className="text-muted-foreground">Loading...</p>}
          {error && <p className="text-destructive">Error: {error}</p>}
          {!loading && !error && opportunities.length === 0 && (
            <p className="text-muted-foreground">No opportunities detected yet. Waiting for scan...</p>
          )}
          {opportunities.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Asset</TableHead>
                  <TableHead>Venues</TableHead>
                  <TableHead>Direction</TableHead>
                  <TableHead className="text-right">Spread</TableHead>
                  <TableHead className="text-right">Ann. Edge</TableHead>
                  <TableHead className="text-right">Entry Cost</TableHead>
                  <TableHead>Risk</TableHead>
                  <TableHead>Confidence</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {opportunities.map((opp) => (
                  <TableRow
                    key={opp.id}
                    className={`cursor-pointer transition-colors ${selectedId === opp.id ? 'bg-accent' : 'hover:bg-muted/50'}`}
                    onClick={() => setSelectedId(selectedId === opp.id ? null : opp.id)}
                  >
                    <TableCell className="font-medium">{opp.asset}</TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {opp.venue_pair.venue_a} / {opp.venue_pair.venue_b}
                    </TableCell>
                    <TableCell className="text-sm">
                      {opp.direction === 'long_a_short_b' ? '⬆ A ⬇ B' : '⬇ A ⬆ B'}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm">{fmtRate(opp.funding_spread)}</TableCell>
                    <TableCell className="text-right font-mono text-sm">{fmtPct(opp.annualized_gross_edge)}</TableCell>
                    <TableCell className="text-right font-mono text-sm">{fmtPct(opp.entry_spread_estimate, 4)}</TableCell>
                    <TableCell>
                      <span className={`text-sm font-medium ${riskColor(opp.risk_tier)}`}>
                        {opp.risk_tier}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Badge variant={confidenceVariant(opp.confidence)}>{opp.confidence}</Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </main>
      </div>

      {/* Detail panel */}
      {selected && (
        <OpportunityPanel
          opportunity={selected}
          lastUpdated={lastUpdated}
          onClose={() => setSelectedId(null)}
        />
      )}
    </div>
  )
}
