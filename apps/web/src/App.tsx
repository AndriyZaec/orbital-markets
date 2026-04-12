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
import { Button } from '@/components/ui/button'

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
  const { opportunities, loading, error } = useOpportunities()
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const selected = opportunities.find((o) => o.id === selectedId) ?? null

  return (
    <div className="dark min-h-screen bg-background">
      <header className="border-b border-border px-6 py-4 flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Orbital Market</h1>
          <p className="text-sm text-muted-foreground">Perp spread opportunities</p>
        </div>
        {selected && (
          <div className="flex items-center gap-4">
            <div className="text-sm text-muted-foreground">
              <span className="font-medium text-foreground">{selected.asset}</span>
              {' · '}
              {selected.venue_pair.venue_a} / {selected.venue_pair.venue_b}
              {' · '}
              {fmtPct(selected.annualized_gross_edge)} ann.
            </div>
            <Button size="lg" disabled={!selected.executable}>
              Open Spread
            </Button>
          </div>
        )}
      </header>

      <main className="p-6">
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
                <TableHead className="text-right">Funding A</TableHead>
                <TableHead className="text-right">Funding B</TableHead>
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
                  className={`cursor-pointer ${selectedId === opp.id ? 'bg-accent' : ''}`}
                  onClick={() => setSelectedId(selectedId === opp.id ? null : opp.id)}
                >
                  <TableCell className="font-medium">{opp.asset}</TableCell>
                  <TableCell className="text-muted-foreground text-sm">
                    {opp.venue_pair.venue_a} / {opp.venue_pair.venue_b}
                  </TableCell>
                  <TableCell className="text-sm">
                    {opp.direction === 'long_a_short_b' ? '⬆ A ⬇ B' : '⬇ A ⬆ B'}
                  </TableCell>
                  <TableCell className="text-right font-mono text-sm">{fmtRate(opp.funding_rate_a)}</TableCell>
                  <TableCell className="text-right font-mono text-sm">{fmtRate(opp.funding_rate_b)}</TableCell>
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
  )
}
