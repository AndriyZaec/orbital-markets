import { useMemo } from 'react'
import { useLiveBalances } from '@/hooks/useLiveBalances'
import { useLivePositions, type LivePosition } from '@/hooks/useLivePositions'
import { useVenueAuthority } from '@/hooks/useVenueAuthority'

// Portfolio is the primary account/position surface for closed-beta users.
// It reuses live balance / live position / venue-authority hooks — no new
// backend endpoints are added here. Analytics remains in the codebase and
// its endpoints keep working; only the nav surface changes.

interface Props {
  onConnectWallets: () => void
  onViewPositions: () => void
}

function fmtUsd(n: number, decimals = 2) {
  if (!Number.isFinite(n)) return '--'
  const sign = n < 0 ? '-' : ''
  const abs = Math.abs(n)
  if (abs >= 1_000_000) return `${sign}$${(abs / 1_000_000).toFixed(2)}M`
  if (abs >= 1_000) return `${sign}$${(abs / 1_000).toFixed(2)}K`
  return `${sign}$${abs.toFixed(decimals)}`
}

function fmtPct(n: number) {
  if (!Number.isFinite(n)) return '--'
  return `${(n * 100).toFixed(2)}%`
}

const OPEN_STATES = new Set(['opening', 'open', 'monitoring', 'closing'])
const DEGRADED_STATES = new Set(['degraded', 'broken_hedge', 'partial', 'stuck', 'error'])

function categorize(p: LivePosition) {
  const s = p.state.toLowerCase()
  if (DEGRADED_STATES.has(s) || p.hedge_mismatch > 0.01) return 'degraded'
  if (OPEN_STATES.has(s)) return 'open'
  return 'closed'
}

export function Portfolio({ onConnectWallets, onViewPositions }: Props) {
  const balances = useLiveBalances()
  const { positions, loading: positionsLoading, error: positionsError } = useLivePositions()
  const { pacifica, hyperliquid, isFullyReady } = useVenueAuthority()

  const connectedVenues = [balances.pacifica, balances.hyperliquid].filter((b) => b.connected).length
  const totalEquity = balances.pacifica.equity + balances.hyperliquid.equity
  const totalAvailable = balances.pacifica.available + balances.hyperliquid.available

  const { openCount, degradedCount, openNotional, unrealizedPnl } = useMemo(() => {
    let openCount = 0
    let degradedCount = 0
    let openNotional = 0
    let unrealizedPnl = 0
    for (const p of positions) {
      const cat = categorize(p)
      if (cat === 'degraded') degradedCount++
      if (cat === 'open' || cat === 'degraded') {
        openCount++
        openNotional += p.notional
        unrealizedPnl += p.total_pnl
      }
    }
    return { openCount, degradedCount, openNotional, unrealizedPnl }
  }, [positions])

  const recentPositions = positions.slice(0, 5)

  // Overall health: degraded > warnings > ok > offline (no venues)
  let health: { label: string; color: string; dot: string }
  if (connectedVenues === 0) {
    health = { label: 'Offline', color: 'text-muted-foreground', dot: 'bg-muted-foreground' }
  } else if (degradedCount > 0) {
    health = { label: `${degradedCount} degraded`, color: 'text-red-400', dot: 'bg-red-400' }
  } else if (!isFullyReady) {
    health = { label: 'Wallets not fully ready', color: 'text-yellow-400', dot: 'bg-yellow-400' }
  } else if (openCount > 0) {
    health = { label: 'Trading', color: 'text-green-400', dot: 'bg-green-400' }
  } else {
    health = { label: 'Ready', color: 'text-green-400', dot: 'bg-green-400' }
  }

  return (
    <div className="max-w-6xl mx-auto px-6 py-6 flex flex-col gap-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-foreground">Portfolio</h1>
          <p className="text-[12px] text-muted-foreground mt-0.5">Live account and position overview.</p>
        </div>
        <div className={`flex items-center gap-1.5 text-[12px] ${health.color}`}>
          <span className={`size-1.5 rounded-full ${health.dot}`} />
          {health.label}
        </div>
      </div>

      {/* Summary tiles */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Tile label="Total Equity" value={connectedVenues > 0 ? fmtUsd(totalEquity) : '--'} hint="Across connected venues" />
        <Tile label="Available" value={connectedVenues > 0 ? fmtUsd(totalAvailable) : '--'} hint="Free margin" />
        <Tile label="Open Notional" value={openCount > 0 ? fmtUsd(openNotional) : '--'} hint={`${openCount} open · ${degradedCount} degraded`} />
        <Tile
          label="Unrealized P&L"
          value={openCount > 0 ? fmtUsd(unrealizedPnl) : '--'}
          hint="Sum across open positions"
          valueClassName={unrealizedPnl > 0 ? 'text-green-400' : unrealizedPnl < 0 ? 'text-red-400' : ''}
        />
      </div>

      {/* Connected accounts */}
      <Section title="Connected Accounts">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          <VenueCard
            name="Pacifica"
            connected={balances.pacifica.connected}
            equity={balances.pacifica.equity}
            available={balances.pacifica.available}
            readiness={pacifica.readiness}
            address={pacifica.address}
          />
          <VenueCard
            name="Hyperliquid"
            connected={balances.hyperliquid.connected}
            equity={balances.hyperliquid.equity}
            available={balances.hyperliquid.available}
            readiness={hyperliquid.readiness}
            address={hyperliquid.address}
          />
        </div>
        {!isFullyReady && (
          <button
            onClick={onConnectWallets}
            className="mt-3 text-[12px] text-blue-400 hover:text-blue-300 transition-colors"
          >
            Connect wallets →
          </button>
        )}
      </Section>

      {/* Live positions */}
      <Section
        title="Live Positions"
        action={
          positions.length > 0 && (
            <button onClick={onViewPositions} className="text-[12px] text-muted-foreground hover:text-foreground">
              Open positions panel →
            </button>
          )
        }
      >
        {positionsError && <p className="text-[12px] text-red-400">Error: {positionsError}</p>}
        {!positionsError && positionsLoading && positions.length === 0 && (
          <p className="text-[12px] text-muted-foreground">Loading positions…</p>
        )}
        {!positionsLoading && positions.length === 0 && (
          <p className="text-[12px] text-muted-foreground">No live positions yet.</p>
        )}
        {recentPositions.length > 0 && (
          <div className="rounded border border-border overflow-hidden">
            <table className="w-full text-[12px]">
              <thead>
                <tr className="text-muted-foreground text-left bg-white/[0.02]">
                  <th className="px-3 py-2 font-medium">Asset</th>
                  <th className="px-3 py-2 font-medium">State</th>
                  <th className="px-3 py-2 font-medium text-right">Notional</th>
                  <th className="px-3 py-2 font-medium text-right">Basis Δ</th>
                  <th className="px-3 py-2 font-medium text-right">P&amp;L</th>
                </tr>
              </thead>
              <tbody>
                {recentPositions.map((p) => {
                  const cat = categorize(p)
                  const stateColor =
                    cat === 'degraded' ? 'text-red-400' : cat === 'open' ? 'text-green-400' : 'text-muted-foreground'
                  return (
                    <tr key={p.id} className="border-t border-border">
                      <td className="px-3 py-2 font-medium text-foreground">{p.asset}</td>
                      <td className={`px-3 py-2 ${stateColor}`}>{p.state}</td>
                      <td className="px-3 py-2 text-right font-mono text-foreground">{fmtUsd(p.notional)}</td>
                      <td className="px-3 py-2 text-right font-mono text-muted-foreground">{fmtPct(p.basis_change)}</td>
                      <td
                        className={`px-3 py-2 text-right font-mono ${
                          p.total_pnl > 0 ? 'text-green-400' : p.total_pnl < 0 ? 'text-red-400' : 'text-foreground'
                        }`}
                      >
                        {fmtUsd(p.total_pnl)}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
        {positions.length > recentPositions.length && (
          <p className="text-[11px] text-muted-foreground/70 mt-2">
            Showing {recentPositions.length} of {positions.length}. Full list in the bottom positions panel.
          </p>
        )}
      </Section>

      {/* Recent activity — sourced from live positions we already fetched.
          Honest label: we don't have a per-fill event log yet; each row is
          the most recent state change on a live position. */}
      <Section title="Recent Activity">
        {positions.length === 0 ? (
          <p className="text-[12px] text-muted-foreground">No live activity yet.</p>
        ) : (
          <div className="rounded border border-border overflow-hidden">
            <table className="w-full text-[12px]">
              <thead>
                <tr className="text-muted-foreground text-left bg-white/[0.02]">
                  <th className="px-3 py-2 font-medium">When</th>
                  <th className="px-3 py-2 font-medium">Asset</th>
                  <th className="px-3 py-2 font-medium">Action</th>
                  <th className="px-3 py-2 font-medium">Venues</th>
                  <th className="px-3 py-2 font-medium text-right">Notional</th>
                  <th className="px-3 py-2 font-medium text-right">P&amp;L</th>
                </tr>
              </thead>
              <tbody>
                {positions.slice(0, 10).map((p) => {
                  const closed = !!p.completed_at
                  const action = closed ? 'Closed' : actionLabel(p.state)
                  const ts = closed ? p.completed_at! : p.updated_at || p.opened_at || p.started_at
                  const isTerminal = closed
                  return (
                    <tr key={p.id} className="border-t border-border">
                      <td className="px-3 py-2 font-mono text-muted-foreground whitespace-nowrap">{fmtRelative(ts)}</td>
                      <td className="px-3 py-2 font-medium text-foreground">{p.asset}</td>
                      <td className={`px-3 py-2 ${isTerminal ? 'text-muted-foreground' : 'text-foreground'}`}>{action}</td>
                      <td className="px-3 py-2 text-muted-foreground capitalize">
                        {p.venue_a} · {p.venue_b}
                      </td>
                      <td className="px-3 py-2 text-right font-mono text-foreground">{fmtUsd(p.notional)}</td>
                      <td
                        className={`px-3 py-2 text-right font-mono ${
                          p.total_pnl > 0 ? 'text-green-400' : p.total_pnl < 0 ? 'text-red-400' : 'text-muted-foreground'
                        }`}
                      >
                        {fmtUsd(p.total_pnl)}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
        <p className="text-[11px] text-muted-foreground/70 mt-2">
          Recent live position activity. A full trade ledger / export is still future work.
        </p>
      </Section>
    </div>
  )
}

function Tile({
  label,
  value,
  hint,
  valueClassName,
}: {
  label: string
  value: string
  hint?: string
  valueClassName?: string
}) {
  return (
    <div className="rounded border border-border bg-white/[0.02] px-3 py-3">
      <p className="text-[11px] text-muted-foreground">{label}</p>
      <p className={`mt-1 text-lg font-mono ${valueClassName ?? 'text-foreground'}`}>{value}</p>
      {hint && <p className="mt-0.5 text-[11px] text-muted-foreground/70">{hint}</p>}
    </div>
  )
}

function Section({ title, action, children }: { title: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <h2 className="text-[13px] font-semibold text-foreground">{title}</h2>
        {action}
      </div>
      {children}
    </div>
  )
}

function VenueCard({
  name,
  connected,
  equity,
  available,
  readiness,
  address,
}: {
  name: string
  connected: boolean
  equity: number
  available: number
  readiness: string
  address: string | null
}) {
  const short = address ? `${address.slice(0, 4)}…${address.slice(-4)}` : null
  const ready = readiness === 'ready'
  return (
    <div className="rounded border border-border bg-white/[0.02] px-3 py-3">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium text-foreground">{name}</span>
        <span
          className={`text-[11px] flex items-center gap-1.5 ${
            ready ? 'text-green-400' : connected ? 'text-yellow-400' : 'text-muted-foreground'
          }`}
        >
          <span
            className={`size-1.5 rounded-full ${
              ready ? 'bg-green-400' : connected ? 'bg-yellow-400' : 'bg-muted-foreground'
            }`}
          />
          {ready ? 'Ready' : connected ? 'Wallet not signing' : 'Not connected'}
        </span>
      </div>
      <div className="mt-2 grid grid-cols-2 gap-2 text-[12px]">
        <div>
          <p className="text-muted-foreground">Equity</p>
          <p className="font-mono text-foreground">{connected ? fmtUsd(equity) : '--'}</p>
        </div>
        <div>
          <p className="text-muted-foreground">Available</p>
          <p className="font-mono text-foreground">{connected ? fmtUsd(available) : '--'}</p>
        </div>
      </div>
      {short && <p className="mt-2 text-[11px] font-mono text-muted-foreground/70">{short}</p>}
    </div>
  )
}
