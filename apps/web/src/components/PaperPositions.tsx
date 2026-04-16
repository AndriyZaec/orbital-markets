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
      return <Badge className="bg-green-500/20 text-green-400 border-green-500/30">open</Badge>
    case 'degraded':
      return <Badge className="bg-red-500/20 text-red-400 border-red-500/30">degraded</Badge>
    case 'closed':
      return <Badge variant="secondary">closed</Badge>
    case 'failed':
      return <Badge className="bg-red-500/20 text-red-400 border-red-500/30">failed</Badge>
    default:
      return <Badge variant="outline">{state}</Badge>
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

  if (loading) return <p className="text-muted-foreground p-6">Loading positions...</p>
  if (error) return <p className="text-destructive p-6">Error: {error}</p>
  if (positions.length === 0) return <p className="text-muted-foreground p-6">No paper positions yet.</p>

  return (
    <div>
      {/* Summary bar */}
      <div className="flex gap-6 px-6 py-3 border-b border-border text-sm">
        <div>
          <span className="text-muted-foreground">Open: </span>
          <span className="font-medium">{openPositions.length}</span>
        </div>
        <div>
          <span className="text-muted-foreground">Price: </span>
          <span className={`font-mono font-medium ${totalPricePnL >= 0 ? 'text-green-400' : 'text-red-400'}`}>
            {fmtPnL(totalPricePnL)}
          </span>
        </div>
        <div>
          <span className="text-muted-foreground">Funding: </span>
          <span className={`font-mono font-medium ${totalFundingPnL >= 0 ? 'text-green-400' : 'text-red-400'}`}>
            {fmtPnL(totalFundingPnL)}
          </span>
        </div>
        <div>
          <span className="text-muted-foreground">Total: </span>
          <span className={`font-mono font-medium ${totalPnL >= 0 ? 'text-green-400' : 'text-red-400'}`}>
            {fmtPnL(totalPnL)}
          </span>
        </div>
        <div>
          <span className="text-muted-foreground">Total: </span>
          <span className="font-medium">{positions.length}</span>
        </div>
      </div>

      {/* Table */}
      <div className="p-6">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Asset</TableHead>
              <TableHead>Venues</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Close Reason</TableHead>
              <TableHead className="text-right">Price P&L</TableHead>
              <TableHead className="text-right">Funding P&L</TableHead>
              <TableHead className="text-right">Total P&L</TableHead>
              <TableHead className="text-right">Mismatch</TableHead>
              <TableHead>Opened</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {positions.map((pos) => (
              <TableRow
                key={pos.id}
                className={`cursor-pointer transition-colors ${selectedId === pos.id ? 'bg-accent' : 'hover:bg-muted/50'}`}
                onClick={() => setSelectedId(selectedId === pos.id ? null : pos.id)}
              >
                <TableCell className="font-medium">{pos.asset}</TableCell>
                <TableCell className="text-muted-foreground text-sm">
                  {pos.venue_pair.venue_a} / {pos.venue_pair.venue_b}
                </TableCell>
                <TableCell>{stateBadge(pos.state)}</TableCell>
                <TableCell className="text-sm text-muted-foreground">
                  {pos.close_reason || '—'}
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
                <TableCell className="text-right font-mono text-sm">
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
