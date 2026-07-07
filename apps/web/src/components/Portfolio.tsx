import { useMemo } from 'react'
import { useLivePositions, type LivePosition } from '@/hooks/useLivePositions'
import { useVenueReadiness, type VenueReadiness } from '@/hooks/useVenueReadiness'

// Portfolio is the primary account/position surface for closed-beta users.
// It reuses live balance / live position / venue-authority hooks — no new
// backend endpoints are added here. Analytics remains in the codebase and
// its endpoints keep working; only the nav surface changes.

interface Props {
  onConnectWallets: () => void
  onViewPositions: () => void
}

// null-safe so disconnected/unknown values render as "--" instead of "$0.00".
function fmtUsd(n: number | null | undefined, decimals = 2) {
  if (n === null || n === undefined || !Number.isFinite(n)) return '--'
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

// State-to-human action label for the activity feed. Falls back to the raw
// state so unknown states still render legibly instead of blanking.
function actionLabel(state: string): string {
  switch (state.toLowerCase()) {
    case 'opening': return 'Opening'
    case 'open': return 'Opened'
    case 'monitoring': return 'Monitoring'
    case 'closing': return 'Closing'
    case 'closed': return 'Closed'
    case 'degraded': return 'Degraded'
    case 'broken_hedge': return 'Broken hedge'
    case 'partial': return 'Partial fill'
    case 'stuck': return 'Stuck'
    case 'error': return 'Error'
    default: return state
  }
}

function fmtRelative(iso: string): string {
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return '--'
  const diff = Date.now() - t
  const sec = Math.round(diff / 1000)
  if (sec < 60) return `${sec}s ago`
  const min = Math.round(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.round(min / 60)
  if (hr < 24) return `${hr}h ago`
  const d = Math.round(hr / 24)
  return `${d}d ago`
}

function categorize(p: LivePosition) {
  const s = p.state.toLowerCase()
  if (DEGRADED_STATES.has(s) || p.hedge_mismatch > 0.01) return 'degraded'
  if (OPEN_STATES.has(s)) return 'open'
  return 'closed'
}

export function Portfolio({ onConnectWallets, onViewPositions }: Props) {
  const { positions, loading: positionsLoading, error: positionsError } = useLivePositions()
  // One typed readiness layer, shared with the header and ConnectAccounts.
  const { pacifica, hyperliquid, aggregate: readiness } = useVenueReadiness()

  // Sum only venues that actually report a value. If NEITHER venue has
  // reported equity, keep the tile as "--" rather than showing $0.00.
  const equityValues = [pacifica.equity, hyperliquid.equity].filter(
    (v): v is number => typeof v === 'number' && Number.isFinite(v),
  )
  const availableValues = [pacifica.available, hyperliquid.available].filter(
    (v): v is number => typeof v === 'number' && Number.isFinite(v),
  )
  const totalEquity = equityValues.length > 0 ? equityValues.reduce((a, b) => a + b, 0) : null
  const totalAvailable = availableValues.length > 0 ? availableValues.reduce((a, b) => a + b, 0) : null

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

  // Overall health: degraded positions win, else fall back to trading
  // readiness (same aggregate as the header and Execute Live).
  let health: { label: string; color: string; dot: string }
  if (degradedCount > 0) {
    health = { label: `${degradedCount} degraded`, color: 'text-red-400', dot: 'bg-red-400' }
  } else if (readiness.statusLabel === 'Not connected') {
    health = { label: 'Not connected', color: 'text-muted-foreground', dot: 'bg-muted-foreground' }
  } else if (!readiness.allReady) {
    health = { label: readiness.statusLabel, color: 'text-yellow-400', dot: 'bg-yellow-400' }
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
        <Tile label="Total Equity" value={fmtUsd(totalEquity)} hint="Across connected venues" />
        <Tile label="Available" value={fmtUsd(totalAvailable)} hint="Free margin" />
        <Tile label="Open Notional" value={openCount > 0 ? fmtUsd(openNotional) : '--'} hint={`${openCount} open · ${degradedCount} degraded`} />
        <Tile
          label="Unrealized P&L"
          value={openCount > 0 ? fmtUsd(unrealizedPnl) : '--'}
          hint="Sum across open positions"
          valueClassName={unrealizedPnl > 0 ? 'text-green-400' : unrealizedPnl < 0 ? 'text-red-400' : ''}
        />
      </div>

      {/* Connected accounts — summary only. Full diagnostics live in Connect Accounts. */}
      <Section
        title="Connected Accounts"
        action={
          !readiness.allReady && (
            <button
              onClick={onConnectWallets}
              className="text-[12px] text-blue-400 hover:text-blue-300 transition-colors"
            >
              Open Connect Accounts →
            </button>
          )
        }
      >
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          <VenueCard readiness={pacifica} />
          <VenueCard readiness={hyperliquid} />
        </div>
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

// Compact status → label/color map. Portfolio deliberately doesn't drill into
// individual wallet/signer/balance rows here — that's ConnectAccounts's job.
// This card summarizes; the "Open Connect Accounts" link handles diagnosis.
const STATUS_VIEW: Record<
  VenueReadiness['status'],
  { label: string; color: string; dot: string }
> = {
  ready:             { label: 'Ready',           color: 'text-green-400',        dot: 'bg-green-400' },
  disconnected:      { label: 'Not connected',   color: 'text-muted-foreground', dot: 'bg-muted-foreground' },
  wallet_connected:  { label: 'Wallet only',     color: 'text-yellow-400',       dot: 'bg-yellow-400' },
  signer_missing:    { label: 'Signer missing',  color: 'text-yellow-400',       dot: 'bg-yellow-400' },
  balance_pending:   { label: 'Balance pending', color: 'text-yellow-400',       dot: 'bg-yellow-400' },
  account_stale:     { label: 'Data stale',      color: 'text-yellow-400',       dot: 'bg-yellow-400' },
  error:             { label: 'Error',           color: 'text-red-400',          dot: 'bg-red-400' },
}

function VenueCard({ readiness }: { readiness: VenueReadiness }) {
  const view = STATUS_VIEW[readiness.status]
  // Show a real number only when we actually have one from the backend.
  // On disconnect (or before the first snapshot) equity/available are null;
  // render "--" rather than an ambiguous $0.00.
  return (
    <div className="rounded border border-border bg-white/[0.02] px-3 py-3">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium text-foreground">{readiness.label}</span>
        <span className={`text-[11px] flex items-center gap-1.5 ${view.color}`}>
          <span className={`size-1.5 rounded-full ${view.dot}`} />
          {view.label}
        </span>
      </div>
      <div className="mt-2 grid grid-cols-2 gap-2 text-[12px]">
        <div>
          <p className="text-muted-foreground">Equity</p>
          <p className="font-mono text-foreground">{fmtUsd(readiness.equity)}</p>
        </div>
        <div>
          <p className="text-muted-foreground">Available</p>
          <p className="font-mono text-foreground">{fmtUsd(readiness.available)}</p>
        </div>
      </div>
      {readiness.shortAddress && (
        <p className="mt-2 text-[11px] font-mono text-muted-foreground/70">{readiness.shortAddress}</p>
      )}
    </div>
  )
}
