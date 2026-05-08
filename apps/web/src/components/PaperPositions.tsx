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
import { Button } from '@/components/ui/button'
import { PositionDetail } from '@/components/PositionDetail'

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
    <>
      {/* Header — fixed */}
      <div className="px-5 py-2 flex items-center gap-3 shrink-0">
        <h2 className="text-sm font-semibold text-foreground">Positions</h2>
        <div className="flex gap-0 ml-1">
          <TabBtn active={tab === 'open'} onClick={() => setTab('open')}>Open</TabBtn>
          <TabBtn active={tab === 'closed'} onClick={() => setTab('closed')}>Closed</TabBtn>
        </div>
      </div>

      {/* Table — scrolls internally */}
      <div className="flex-1 overflow-auto min-h-0">
        {loading && <p className="text-muted-foreground text-xs px-5 py-3">Loading...</p>}
        {error && <p className="text-destructive text-xs px-5 py-3">Error: {error}</p>}

        {!loading && !error && displayed.length === 0 && (
          <p className="text-muted-foreground text-xs px-5 py-3">
            {positions.length === 0 ? 'No positions yet.' : `No ${tab} positions.`}
          </p>
        )}

        {!loading && !error && displayed.length > 0 && (
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
                  <TableCell className="font-medium text-foreground text-sm py-2">{pos.asset}</TableCell>
                  <TableCell className="font-mono text-xs text-foreground py-2">
                    ${pos.target_notional.toFixed(2)}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-foreground py-2">
                    {(pos.current_spread * 100).toFixed(4)}%
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs text-foreground py-2">
                    {(pos.current_spread * 8760 * 100 / 100).toFixed(2)}%
                  </TableCell>
                  <TableCell className={`text-right font-mono text-xs py-2 ${pos.total_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                    {fmtPnL(pos.total_pnl)}
                  </TableCell>
                  <TableCell className={`text-right font-mono text-xs py-2 ${pos.realized_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                    {pos.state === 'closed' ? fmtPnL(pos.realized_pnl) : '—'}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs text-muted-foreground py-2">
                    {pos.entry_basis ? (pos.entry_basis * 100).toFixed(4) + '%' : '—'}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground py-2">
                    {fmtTime(pos.opened_at)}
                  </TableCell>
                  <TableCell className="py-2">
                    {(pos.state === 'open' || pos.state === 'degraded') && (
                      <Button
                        variant="destructive"
                        size="sm"
                        className="h-6 text-[11px] px-2"
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
      </div>

      {selected && (
        <PositionDetail position={selected} onClose={() => setSelectedId(null)} />
      )}
    </>
  )
}

function TH({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <TableHead className={right ? 'text-right' : ''}>
      {children}
    </TableHead>
  )
}

function TabBtn({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`text-xs font-medium px-2 pb-0.5 border-b-2 transition-colors ${
        active
          ? 'text-foreground border-foreground'
          : 'text-muted-foreground border-transparent hover:text-foreground'
      }`}
    >
      {children}
    </button>
  )
}
