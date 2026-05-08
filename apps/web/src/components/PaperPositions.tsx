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

export function PaperPositions() {
  const { positions, loading, error, closePosition } = usePaperPositions()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [closing, setClosing] = useState<string | null>(null)
  const [tab, setTab] = useState<'open' | 'closed'>('open')

  const selected = positions.find((p) => p.id === selectedId) ?? null

  const openPositions = positions.filter((p) => p.state === 'open' || p.state === 'degraded')
  const closedPositions = positions.filter((p) => p.state === 'closed' || p.state === 'failed')
  const displayed = tab === 'open' ? openPositions : closedPositions

  const handleClose = async (id: string) => {
    setClosing(id)
    try {
      await closePosition(id)
    } catch {
      // error surfaced via hook
    } finally {
      setClosing(null)
    }
  }

  return (
    <div>
      {/* Header */}
      <div className="px-5 pt-5 pb-3 flex items-center gap-4">
        <h2 className="text-base font-semibold text-foreground">Positions</h2>
        <div className="flex gap-0">
          <button
            onClick={() => setTab('open')}
            className={`text-sm font-medium px-2 pb-1 border-b-2 transition-colors ${
              tab === 'open'
                ? 'text-foreground border-foreground'
                : 'text-muted-foreground border-transparent hover:text-foreground'
            }`}
          >
            Open
          </button>
          <button
            onClick={() => setTab('closed')}
            className={`text-sm font-medium px-2 pb-1 border-b-2 transition-colors ${
              tab === 'closed'
                ? 'text-foreground border-foreground'
                : 'text-muted-foreground border-transparent hover:text-foreground'
            }`}
          >
            Closed
          </button>
        </div>
      </div>

      {loading && <p className="text-muted-foreground text-sm px-5 py-4">Loading...</p>}
      {error && <p className="text-destructive text-sm px-5 py-4">Error: {error}</p>}

      {!loading && !error && (
        <>
          {displayed.length === 0 ? (
            <p className="text-muted-foreground text-sm px-5 py-4">
              {positions.length === 0 ? 'No positions yet.' : `No ${tab} positions.`}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="border-border hover:bg-transparent">
                  <TH>Asset</TH>
                  <TH>Size</TH>
                  <TH>1h Spread</TH>
                  <TH right>APR</TH>
                  <TH right>UPnL</TH>
                  <TH right>Est Close PnL</TH>
                  <TH right>Breakeven</TH>
                  <TH>Duration</TH>
                  <TH>Actions</TH>
                </TableRow>
              </TableHeader>
              <TableBody>
                {displayed.map((pos) => (
                  <TableRow
                    key={pos.id}
                    className="cursor-pointer transition-colors border-border hover:bg-white/[0.02]"
                    onClick={() => setSelectedId(selectedId === pos.id ? null : pos.id)}
                  >
                    <TableCell className="font-medium text-foreground">
                      <div className="flex items-center gap-2">
                        {pos.asset}
                        {(pos.state === 'degraded' || pos.state === 'failed') && stateBadge(pos.state)}
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-sm text-foreground">
                      ${pos.target_notional.toFixed(2)}
                    </TableCell>
                    <TableCell className="font-mono text-sm text-foreground">
                      {(pos.current_spread * 100).toFixed(4)}%
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm text-foreground">
                      {(pos.current_spread * 100 * 8760 / 100).toFixed(2)}%
                    </TableCell>
                    <TableCell className={`text-right font-mono text-sm ${pos.total_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                      {fmtPnL(pos.total_pnl)}
                    </TableCell>
                    <TableCell className={`text-right font-mono text-sm ${pos.realized_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                      {pos.state === 'closed' ? fmtPnL(pos.realized_pnl) : '—'}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm text-muted-foreground">
                      {pos.entry_basis ? (pos.entry_basis * 100).toFixed(4) + '%' : '—'}
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
                          {closing === pos.id ? '...' : 'Close'}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </>
      )}

      {selected && (
        <PositionDetail position={selected} onClose={() => setSelectedId(null)} />
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
