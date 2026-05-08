import { useState } from 'react'
import { usePaperPositions } from '@/hooks/usePaperPositions'
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
import { PositionDetail } from '@/components/PositionDetail'

function stateBadge(state: string) {
  switch (state) {
    case 'open':
      return <Badge className="bg-green-500/15 text-green-400 border-green-500/30 text-[11px]">open</Badge>
    case 'degraded':
      return <Badge className="bg-red-500/15 text-red-400 border-red-500/30 text-[11px]">degraded</Badge>
    case 'closed':
      return <Badge variant="secondary" className="text-[11px]">closed</Badge>
    case 'failed':
      return <Badge className="bg-red-500/15 text-red-400 border-red-500/30 text-[11px]">failed</Badge>
    default:
      return <Badge variant="outline" className="text-[11px]">{state}</Badge>
  }
}

function fmtPnL(n: number) {
  const sign = n >= 0 ? '+' : ''
  return sign + n.toFixed(4)
}

function fmtTime(s: string | null) {
  if (!s) return '—'
  return new Date(s).toLocaleTimeString()
}

function closeReasonLabel(reason: string) {
  if (!reason) return <span className="text-muted-foreground">—</span>
  const map: Record<string, { label: string; color: string }> = {
    manual: { label: 'Manual', color: 'text-muted-foreground' },
    edge_collapse: { label: 'Edge Collapse', color: 'text-yellow-400' },
    degraded: { label: 'Degraded', color: 'text-orange-400' },
    max_duration: { label: 'Max Duration', color: 'text-muted-foreground' },
    liquidation_risk: { label: 'Liquidation Risk', color: 'text-red-400' },
  }
  const entry = map[reason] ?? { label: reason, color: 'text-muted-foreground' }
  return <span className={`font-medium ${entry.color}`}>{entry.label}</span>
}

export function PaperPositions() {
  const { positions, loading, error, closePosition } = usePaperPositions()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [closing, setClosing] = useState<string | null>(null)

  const selected = positions.find((p) => p.id === selectedId) ?? null

  const openPositions = positions.filter((p) => p.state === 'open' || p.state === 'degraded')
  const totalPricePnL = positions.reduce((sum, p) => sum + p.price_pnl, 0)
  const totalFundingPnL = positions.reduce((sum, p) => sum + p.funding_pnl, 0)
  const totalPnL = positions.reduce((sum, p) => sum + p.total_pnl, 0)

  const handleClose = async (id: string) => {
    setClosing(id)
    try {
      await closePosition(id)
    } catch {
      // error is surfaced via the hook
    } finally {
      setClosing(null)
    }
  }

  if (loading) return <p className="text-muted-foreground text-sm px-5 py-8">Loading positions...</p>
  if (error) return <p className="text-destructive text-sm px-5 py-8">Error: {error}</p>
  if (positions.length === 0) return <p className="text-muted-foreground text-sm px-5 py-8">No paper positions yet.</p>

  return (
    <div className="flex flex-col flex-1">
      {/* Header + Summary */}
      <div className="px-5 pt-5 pb-3 border-b border-border">
        <h2 className="text-base font-semibold text-foreground mb-3">Positions</h2>
        <div className="flex gap-6 text-sm">
          <SummaryItem label="Open" value={String(openPositions.length)} />
          <SummaryItem label="Price" value={fmtPnL(totalPricePnL)} color={totalPricePnL >= 0 ? 'text-green-400' : 'text-red-400'} mono />
          <SummaryItem label="Funding" value={fmtPnL(totalFundingPnL)} color={totalFundingPnL >= 0 ? 'text-green-400' : 'text-red-400'} mono />
          <SummaryItem label="Total" value={fmtPnL(totalPnL)} color={totalPnL >= 0 ? 'text-green-400' : 'text-red-400'} mono />
          <SummaryItem label="All" value={String(positions.length)} />
        </div>
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto">
        <Table>
          <TableHeader>
            <TableRow className="border-border hover:bg-transparent">
              <TableHead className="text-muted-foreground font-medium text-xs uppercase tracking-wider">Asset</TableHead>
              <TableHead className="text-muted-foreground font-medium text-xs uppercase tracking-wider">Venues</TableHead>
              <TableHead className="text-muted-foreground font-medium text-xs uppercase tracking-wider">State</TableHead>
              <TableHead className="text-muted-foreground font-medium text-xs uppercase tracking-wider">Close Reason</TableHead>
              <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Price P&L</TableHead>
              <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Funding P&L</TableHead>
              <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Total P&L</TableHead>
              <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Mismatch</TableHead>
              <TableHead className="text-muted-foreground font-medium text-xs uppercase tracking-wider">Opened</TableHead>
              <TableHead className="w-20"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {positions.map((pos) => (
              <TableRow
                key={pos.id}
                className={`cursor-pointer transition-colors border-border ${
                  selectedId === pos.id
                    ? 'bg-white/[0.04] border-l-2 border-l-blue-500'
                    : 'hover:bg-white/[0.02]'
                }`}
                onClick={() => setSelectedId(selectedId === pos.id ? null : pos.id)}
              >
                <TableCell className="font-medium text-foreground">{pos.asset}</TableCell>
                <TableCell className="text-muted-foreground text-sm">
                  {pos.venue_pair.venue_a} / {pos.venue_pair.venue_b}
                </TableCell>
                <TableCell>{stateBadge(pos.state)}</TableCell>
                <TableCell className="text-sm">
                  {closeReasonLabel(pos.close_reason)}
                </TableCell>
                <TableCell className={`text-right font-mono text-sm ${pos.price_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                  {fmtPnL(pos.price_pnl)}
                </TableCell>
                <TableCell className={`text-right font-mono text-sm ${pos.funding_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                  {fmtPnL(pos.funding_pnl)}
                </TableCell>
                <TableCell className={`text-right font-mono text-sm ${pos.total_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                  {fmtPnL(pos.total_pnl)}
                </TableCell>
                <TableCell className="text-right font-mono text-sm text-muted-foreground">
                  {(pos.hedge_mismatch * 100).toFixed(1)}%
                </TableCell>
                <TableCell className="text-sm text-muted-foreground">
                  {fmtTime(pos.opened_at)}
                </TableCell>
                <TableCell>
                  {(pos.state === 'open' || pos.state === 'degraded') && (
                    <Button
                      variant="destructive"
                      size="sm"
                      className="h-7 text-xs"
                      disabled={closing === pos.id}
                      onClick={(e) => {
                        e.stopPropagation()
                        handleClose(pos.id)
                      }}
                    >
                      {closing === pos.id ? 'Closing...' : 'Close'}
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Detail modal */}
      {selected && (
        <PositionDetail
          position={selected}
          onClose={() => setSelectedId(null)}
        />
      )}
    </div>
  )
}

function SummaryItem({ label, value, color, mono }: { label: string; value: string; color?: string; mono?: boolean }) {
  return (
    <div>
      <span className="text-muted-foreground text-xs">{label} </span>
      <span className={`text-xs font-medium ${color ?? 'text-foreground'} ${mono ? 'font-mono' : ''}`}>
        {value}
      </span>
    </div>
  )
}
