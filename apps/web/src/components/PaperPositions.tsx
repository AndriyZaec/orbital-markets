import { useState } from 'react'
import { usePaperPositions } from '@/hooks/usePaperPositions'
import type { PaperPosition, Fill } from '@/hooks/usePaperPositions'
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
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'

function fmtPnL(n: number) {
  const sign = n >= 0 ? '+' : ''
  if (Math.abs(n) >= 1000) return sign + '$' + Math.abs(n).toFixed(0)
  return sign + '$' + n.toFixed(2)
}

function fmtUsd(n: number) {
  if (n >= 1_000_000) return '$' + (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return '$' + (n / 1_000).toFixed(1) + 'K'
  return '$' + n.toFixed(0)
}

function fmtPct(n: number, decimals = 4) {
  return (n * 100).toFixed(decimals) + '%'
}

function fmtDuration(openedAt: string | null) {
  if (!openedAt) return '—'
  const ms = Date.now() - new Date(openedAt).getTime()
  const mins = Math.floor(ms / 60_000)
  if (mins < 1) return '< 1m'
  const hours = Math.floor(mins / 60)
  const days = Math.floor(hours / 24)
  if (days > 0) return `${days}d ${hours % 24}h`
  if (hours > 0) return `${hours}h ${mins % 60}m`
  return `${mins}m`
}

function stateDot(state: string) {
  switch (state) {
    case 'open': return 'bg-green-400'
    case 'degraded': return 'bg-orange-400'
    case 'failed': return 'bg-red-400'
    case 'closed': return 'bg-muted-foreground'
    default: return 'bg-yellow-400'
  }
}

function worstLiqRisk(pos: PaperPosition): Fill['liq_risk'] {
  const order: Fill['liq_risk'][] = ['', 'safe', 'elevated', 'warning', 'critical']
  const a = pos.leg_1_fill?.liq_risk ?? ''
  const b = pos.leg_2_fill?.liq_risk ?? ''
  return order.indexOf(a) > order.indexOf(b) ? a : b
}

function liqRiskStyle(risk: Fill['liq_risk']) {
  switch (risk) {
    case 'safe': return 'text-green-400 bg-green-500/10'
    case 'elevated': return 'text-blue-400 bg-blue-500/10'
    case 'warning': return 'text-yellow-400 bg-yellow-500/10'
    case 'critical': return 'text-red-400 bg-red-500/10'
    default: return 'text-muted-foreground bg-white/[0.03]'
  }
}

const venueLogos: Record<string, string> = { pacifica: pacificaLogo, hyperliquid: hlLogo }

function VenueIcon({ venue }: { venue: string }) {
  const logo = venueLogos[venue]
  if (logo) return <img src={logo} alt={venue} className="size-4 rounded-sm" />
  return <span className="text-[10px] text-muted-foreground uppercase">{venue.slice(0, 3)}</span>
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
      {/* Header */}
      <div className="px-5 py-2 flex items-center gap-3 shrink-0 bg-[#080b12]">
        <h2 className="text-sm font-semibold text-foreground">Positions</h2>
        <div className="flex gap-0 ml-1">
          <TabBtn active={tab === 'open'} onClick={() => setTab('open')}>
            Open{openPositions.length > 0 && <span className="ml-1 text-muted-foreground">({openPositions.length})</span>}
          </TabBtn>
          <TabBtn active={tab === 'closed'} onClick={() => setTab('closed')}>
            Closed{closedPositions.length > 0 && <span className="ml-1 text-muted-foreground">({closedPositions.length})</span>}
          </TabBtn>
        </div>
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto min-h-0 bg-[#080b12]">
        {loading && <p className="text-muted-foreground text-xs px-5 py-3">Loading...</p>}
        {error && <p className="text-destructive text-xs px-5 py-3">Error: {error}</p>}

        {!loading && !error && displayed.length === 0 && (
          <p className="text-muted-foreground text-xs px-5 py-3">
            {positions.length === 0 ? 'No positions yet.' : `No ${tab} positions.`}
          </p>
        )}

        {!loading && !error && displayed.length > 0 && (
          <Table>
            <TableHeader className="sticky top-0 z-10">
              <TableRow className="border-border hover:bg-transparent bg-[#080b12]">
                <TH>Asset</TH>
                <TH>Direction</TH>
                <TH right>Size</TH>
                <TH right>Lev</TH>
                <TH right>Entry</TH>
                <TH right>Current</TH>
                <TH right>Fund. PnL</TH>
                <TH right>Price PnL</TH>
                <TH right>Total PnL</TH>
                <TH>Liq</TH>
                <TH>Duration</TH>
                <TH>Actions</TH>
              </TableRow>
            </TableHeader>
            <TableBody>
              {displayed.map((pos) => {
                const isDegraded = pos.state === 'degraded'
                const liqRisk = worstLiqRisk(pos)
                const isLongA = pos.direction === 'long_a_short_b'
                const longVenue = isLongA ? pos.venue_pair.venue_a : pos.venue_pair.venue_b
                const shortVenue = isLongA ? pos.venue_pair.venue_b : pos.venue_pair.venue_a

                return (
                  <TableRow
                    key={pos.id}
                    className={`cursor-pointer transition-colors border-border hover:bg-white/[0.02] ${isDegraded ? 'border-l-2 border-l-orange-400/50' : ''}`}
                    onClick={() => setSelectedId(selectedId === pos.id ? null : pos.id)}
                  >
                    <TableCell className="py-2">
                      <div className="flex items-center gap-2">
                        <div className={`size-1.5 rounded-full ${stateDot(pos.state)}`} />
                        <span className="font-medium text-foreground text-sm">{pos.asset}</span>
                      </div>
                    </TableCell>
                    <TableCell className="py-2">
                      <div className="flex items-center gap-1">
                        <VenueIcon venue={longVenue} />
                        <span className="text-muted-foreground text-[10px]">→</span>
                        <VenueIcon venue={shortVenue} />
                      </div>
                    </TableCell>
                    <TC>{fmtUsd(pos.target_notional)}</TC>
                    <TC>{pos.leverage.leverage}x</TC>
                    <TC>{fmtPct(pos.entry_spread)}</TC>
                    <TC>{fmtPct(pos.current_spread)}</TC>
                    <TC negative={pos.funding_pnl < 0}>{fmtPnL(pos.funding_pnl)}</TC>
                    <TC negative={pos.price_pnl < 0}>{fmtPnL(pos.price_pnl)}</TC>
                    <TableCell className={`text-right font-mono text-xs font-semibold py-2 ${pos.total_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`} style={{ textAlign: 'right' }}>
                      {fmtPnL(pos.total_pnl)}
                    </TableCell>
                    <TableCell className="py-2">
                      {liqRisk && liqRisk !== '' ? (
                        <span className={`text-[10px] font-medium px-1.5 py-0.5 rounded ${liqRiskStyle(liqRisk)}`}>
                          {liqRisk}
                        </span>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground py-2">
                      {pos.state === 'closed' ? fmtDuration(pos.opened_at) : fmtDuration(pos.opened_at)}
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
                )
              })}
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

function TC({ children, negative }: { children: React.ReactNode; negative?: boolean }) {
  return (
    <TableCell
      className={`font-mono text-xs py-2 ${negative ? 'text-red-400' : 'text-foreground'}`}
      style={{ textAlign: 'right' }}
    >
      {children}
    </TableCell>
  )
}

function TH({ children, right }: { children: React.ReactNode; right?: boolean }) {
  return (
    <TableHead style={{ textAlign: right ? 'right' : 'left' }}>
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
