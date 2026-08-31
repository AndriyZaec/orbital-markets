import { useEffect, useMemo, useState } from 'react'
import { EyeIcon, EyeOffIcon, Share2Icon } from 'lucide-react'
import { useLivePositions, type LivePosition } from '@/hooks/useLivePositions'
import { useVenueReadiness, type VenueReadiness } from '@/hooks/useVenueReadiness'
import { AssetIcon } from '@/components/AssetIcon'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'
import { portfolioPositionCategory } from '@/lib/portfolio-position'
import {
  portfolioPerformance,
  type PortfolioPerformance,
} from '@/lib/portfolio-performance'

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

function fmtReturn(n: number | null) {
  if (n === null || !Number.isFinite(n)) return '--'
  const percent = n * 100
  const decimals = Math.abs(percent) >= 100 ? 0 : 2
  return `${percent >= 0 ? '+' : ''}${percent.toFixed(decimals)}%`
}

const MASKED_VALUE = '****'

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

function fmtRelative(iso: string, now: number): string {
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return '--'
  const diff = Math.max(0, now - t)
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return `${sec}s ago`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h ago`
  const d = Math.floor(hr / 24)
  return `${d}d ago`
}

function categorize(p: LivePosition) {
  return portfolioPositionCategory(p.state, p.hedge_mismatch)
}

export function Portfolio({ onConnectWallets, onViewPositions }: Props) {
  const { positions, loading: positionsLoading, error: positionsError } = useLivePositions()
  // One typed readiness layer, shared with the header and ConnectAccounts.
  const { pacifica, hyperliquid, aggregate: readiness } = useVenueReadiness()
  const [privateView, setPrivateView] = useState(false)
  const [sharing, setSharing] = useState(false)
  const [openedAt] = useState(() => Date.now())
  const activityAccountKey = `${pacifica.address ?? ''}|${hyperliquid.address ?? ''}`
  const [activitySnapshot, setActivitySnapshot] = useState<{
    accountKey: string
    positions: LivePosition[]
  } | null>(null)

  useEffect(() => {
    if (positionsLoading || activitySnapshot?.accountKey === activityAccountKey) return
    setActivitySnapshot({ accountKey: activityAccountKey, positions: positions.slice(0, 10) })
  }, [activityAccountKey, activitySnapshot?.accountKey, positions, positionsLoading])

  const recentActivity = activitySnapshot?.accountKey === activityAccountKey
    ? activitySnapshot.positions
    : []

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

  const { openCount, degradedCount, openNotional, unrealizedPnl, realizedPnl, closedCount } = useMemo(() => {
    let openCount = 0
    let degradedCount = 0
    let openNotional = 0
    let unrealizedPnl = 0
    let realizedPnl = 0
    let closedCount = 0
    for (const p of positions) {
      const cat = categorize(p)
      if (cat === 'degraded') degradedCount++
      if (cat === 'open' || cat === 'degraded') {
        openCount++
        openNotional += p.notional
        unrealizedPnl += p.total_pnl
      } else if (p.state.toLowerCase() === 'closed') {
        closedCount++
        realizedPnl += p.total_pnl
      }
    }
    return { openCount, degradedCount, openNotional, unrealizedPnl, realizedPnl, closedCount }
  }, [positions])

  const recentPositions = positions.slice(0, 5)
  const unrealizedPerformance = useMemo(
    () => portfolioPerformance(positions.filter((position) => {
      const category = categorize(position)
      return category === 'open' || category === 'degraded'
    }), openedAt),
    [positions, openedAt],
  )
  const realizedPerformance = useMemo(
    () => portfolioPerformance(
      positions.filter((position) => position.state.toLowerCase() === 'closed'),
      openedAt,
    ),
    [positions, openedAt],
  )
  const sharedPerformance = realizedPerformance.value !== null ? realizedPerformance : unrealizedPerformance

  // Portfolio health describes positions only. Account readiness belongs to
  // the Accounts button and Connected Accounts cards.
  let health: { label: string; color: string; dot: string }
  if (degradedCount > 0) {
    health = { label: `${degradedCount} degraded`, color: 'text-red-400', dot: 'bg-red-400' }
  } else if (openCount > 0) {
    health = { label: 'Trading', color: 'text-green-400', dot: 'bg-green-400' }
  } else {
    health = { label: 'Idle', color: 'text-muted-foreground', dot: 'bg-muted-foreground' }
  }

  const unrealizedTone = unrealizedPerformance.value === null
    ? 'plain'
    : unrealizedPerformance.value > 0
      ? 'green'
      : unrealizedPerformance.value < 0
        ? 'rose'
        : 'plain'

  const handleShare = async () => {
    if (sharedPerformance.value === null || sharing) return
    setSharing(true)
    try {
      await sharePerformanceCard(sharedPerformance)
    } catch (error) {
      if (!(error instanceof DOMException && error.name === 'AbortError')) console.error('Unable to share PnL card', error)
    } finally {
      setSharing(false)
    }
  }

  return (
    <div className="max-w-6xl mx-auto px-6 py-6 flex flex-col gap-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <h1 className="text-xl font-bold text-foreground">Portfolio</h1>
          <button
            type="button"
            onClick={() => setPrivateView((value) => !value)}
            title={privateView ? 'Show portfolio amounts' : 'Hide amounts and show returns'}
            aria-label={privateView ? 'Show portfolio amounts' : 'Hide portfolio amounts'}
            aria-pressed={privateView}
            className={`flex size-8 items-center justify-center transition-colors ${privateView
              ? 'text-cyan-400 hover:text-cyan-300'
              : 'text-muted-foreground hover:text-foreground'}`}
          >
            {privateView ? <EyeOffIcon className="size-4" /> : <EyeIcon className="size-4" />}
          </button>
        </div>
        <div className="flex items-center gap-3">
          {privateView && sharedPerformance.value !== null && (
            <button
              type="button"
              onClick={handleShare}
              disabled={sharing}
              className="flex items-center gap-1.5 text-[12px] text-cyan-400 transition-colors hover:text-cyan-300 disabled:opacity-50"
              aria-label="Share annualized return"
            >
              <Share2Icon className="size-3.5" />
              <span className="hidden sm:inline">{sharing ? 'Preparing…' : 'Share'}</span>
            </button>
          )}
          <div className={`flex items-center gap-1.5 text-[12px] ${health.color}`}>
            <span className={`size-1.5 rounded-full ${health.dot}`} />
            {health.label}
          </div>
        </div>
      </div>

      {/* Summary tiles */}
      <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
        <Tile label="Total Equity" value={privateView ? MASKED_VALUE : fmtUsd(totalEquity)} hint="Across connected venues" tone="cyan" />
        <Tile label="Available" value={privateView ? MASKED_VALUE : fmtUsd(totalAvailable)} hint="Free margin" />
        <Tile label="Open Notional" value={privateView ? MASKED_VALUE : openCount > 0 ? fmtUsd(openNotional) : '--'} hint={`${openCount} open · ${degradedCount} degraded`} />
        <Tile
          label={privateView ? unrealizedPerformance.value === null || unrealizedPerformance.annualized ? 'uPnL Annualized Return' : 'uPnL Return' : 'Unrealized P&L'}
          value={privateView ? fmtReturn(unrealizedPerformance.value) : openCount > 0 ? fmtUsd(unrealizedPnl) : '--'}
          hint={privateView ? 'On deployed capital' : 'Sum across open positions'}
          valueClassName={privateView
            ? unrealizedPerformance.value !== null && unrealizedPerformance.value > 0 ? 'text-green-400' : unrealizedPerformance.value !== null && unrealizedPerformance.value < 0 ? 'text-red-400' : ''
            : unrealizedPnl > 0 ? 'text-green-400' : unrealizedPnl < 0 ? 'text-red-400' : ''}
          tone={privateView ? unrealizedTone : unrealizedPnl > 0 ? 'green' : unrealizedPnl < 0 ? 'rose' : 'plain'}
        />
        <Tile
          label={privateView ? realizedPerformance.annualized ? 'Annualized Return' : 'Realized Return' : 'Realized P&L'}
          value={privateView ? fmtReturn(realizedPerformance.value) : closedCount > 0 ? fmtUsd(realizedPnl) : '--'}
          hint={privateView ? 'On deployed capital' : `${closedCount} closed position${closedCount === 1 ? '' : 's'}`}
          valueClassName={privateView
            ? realizedPerformance.value !== null && realizedPerformance.value > 0 ? 'text-green-400' : realizedPerformance.value !== null && realizedPerformance.value < 0 ? 'text-red-400' : ''
            : realizedPnl > 0 ? 'text-green-400' : realizedPnl < 0 ? 'text-red-400' : ''}
          tone={privateView
            ? realizedPerformance.value !== null && realizedPerformance.value > 0 ? 'green' : realizedPerformance.value !== null && realizedPerformance.value < 0 ? 'rose' : 'plain'
            : realizedPnl > 0 ? 'green' : realizedPnl < 0 ? 'rose' : 'plain'}
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
          <VenueCard readiness={pacifica} maskAmounts={privateView} />
          <VenueCard readiness={hyperliquid} maskAmounts={privateView} />
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
                  <th className="px-3 py-2 font-medium text-right">{privateView ? 'Return' : 'P&L'}</th>
                </tr>
              </thead>
              <tbody>
                {recentPositions.map((p) => {
                  const cat = categorize(p)
                  const stateColor =
                    cat === 'degraded' ? 'text-red-400' : cat === 'open' ? 'text-green-400' : 'text-muted-foreground'
                  return (
                    <tr key={p.id} className="border-t border-border">
                      <td className="px-3 py-2 font-medium text-foreground">
                        <div className="flex items-center gap-2"><AssetIcon asset={p.asset} size="sm" />{p.asset}</div>
                      </td>
                      <td className={`px-3 py-2 ${stateColor}`}>{p.state}</td>
                      <td className="px-3 py-2 text-right font-mono text-foreground">{privateView ? MASKED_VALUE : fmtUsd(p.notional)}</td>
                      <td className="px-3 py-2 text-right font-mono text-muted-foreground">{fmtPct(p.basis_change)}</td>
                      <td
                        className={`px-3 py-2 text-right font-mono ${
                          p.total_pnl > 0 ? 'text-green-400' : p.total_pnl < 0 ? 'text-red-400' : 'text-foreground'
                        }`}
                      >
                        {privateView
                          ? fmtReturn(portfolioPerformance([p], openedAt).value)
                          : fmtUsd(p.total_pnl)}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Section>

      {/* Recent activity — sourced from live positions we already fetched.
          Honest label: we don't have a per-fill event log yet; each row is
          the most recent state change on a live position. */}
      <Section title="Recent Activity">
        {recentActivity.length === 0 ? (
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
                  <th className="px-3 py-2 font-medium text-right">{privateView ? 'Return' : 'P&L'}</th>
                </tr>
              </thead>
              <tbody>
                {recentActivity.map((p) => {
                  const closed = !!p.completed_at
                  const action = closed ? 'Closed' : actionLabel(p.state)
                  const ts = closed ? p.completed_at! : p.updated_at || p.opened_at || p.started_at
                  const isTerminal = closed
                  return (
                    <tr key={p.id} className="border-t border-border">
                      <td className="px-3 py-2 font-mono text-muted-foreground whitespace-nowrap">{fmtRelative(ts, openedAt)}</td>
                      <td className="px-3 py-2 font-medium text-foreground">
                        <div className="flex items-center gap-2"><AssetIcon asset={p.asset} size="sm" />{p.asset}</div>
                      </td>
                      <td className={`px-3 py-2 ${isTerminal ? 'text-muted-foreground' : 'text-foreground'}`}>{action}</td>
                      <td className="px-3 py-2 text-muted-foreground capitalize">
                        {p.venue_a} · {p.venue_b}
                      </td>
                      <td className="px-3 py-2 text-right font-mono text-foreground">{privateView ? MASKED_VALUE : fmtUsd(p.notional)}</td>
                      <td
                        className={`px-3 py-2 text-right font-mono ${
                          p.total_pnl > 0 ? 'text-green-400' : p.total_pnl < 0 ? 'text-red-400' : 'text-muted-foreground'
                        }`}
                      >
                        {privateView
                          ? fmtReturn(portfolioPerformance([p], openedAt).value)
                          : fmtUsd(p.total_pnl)}
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

async function sharePerformanceCard(performance: PortfolioPerformance): Promise<void> {
  const canvas = document.createElement('canvas')
  canvas.width = 1200
  canvas.height = 630
  const ctx = canvas.getContext('2d')
  if (!ctx || performance.value === null) throw new Error('Unable to create performance card')

  const background = ctx.createLinearGradient(0, 0, canvas.width, canvas.height)
  background.addColorStop(0, '#070a10')
  background.addColorStop(0.6, '#0a111b')
  background.addColorStop(1, '#07171a')
  ctx.fillStyle = background
  ctx.fillRect(0, 0, canvas.width, canvas.height)

  const glow = ctx.createRadialGradient(980, 80, 0, 980, 80, 520)
  glow.addColorStop(0, 'rgba(34, 211, 238, 0.20)')
  glow.addColorStop(1, 'rgba(34, 211, 238, 0)')
  ctx.fillStyle = glow
  ctx.fillRect(0, 0, canvas.width, canvas.height)

  ctx.fillStyle = '#f8fafc'
  ctx.font = '600 34px Geist, system-ui, sans-serif'
  ctx.fillText('ORBITAL', 72, 82)
  ctx.fillStyle = '#67e8f9'
  ctx.font = '500 19px Geist, system-ui, sans-serif'
  ctx.fillText('MARKET-NEUTRAL PERFORMANCE', 72, 124)

  ctx.fillStyle = '#94a3b8'
  ctx.font = '500 22px Geist, system-ui, sans-serif'
  ctx.fillText(performance.annualized ? 'Annualized Return' : 'Return on Deployed Capital', 72, 220)
  ctx.fillStyle = performance.value >= 0 ? '#4ade80' : '#fb7185'
  ctx.font = '700 92px Geist, system-ui, sans-serif'
  ctx.fillText(fmtReturn(performance.value), 66, 326)
  ctx.fillStyle = '#94a3b8'
  ctx.font = '400 20px Geist, system-ui, sans-serif'
  ctx.fillText('Realized + unrealized P&L on deployed capital', 72, 370)

  const assets = performance.byAsset.slice(0, 4)
  if (assets.length > 0) {
    ctx.fillStyle = '#64748b'
    ctx.font = '500 17px Geist, system-ui, sans-serif'
    ctx.fillText('BY ASSET', 730, 215)

    assets.forEach((asset, index) => {
      const y = 265 + index * 62
      ctx.fillStyle = '#e2e8f0'
      ctx.font = '600 22px Geist, system-ui, sans-serif'
      ctx.fillText(asset.asset, 730, y)
      ctx.fillStyle = asset.value >= 0 ? '#4ade80' : '#fb7185'
      ctx.font = '600 22px Geist, system-ui, sans-serif'
      ctx.textAlign = 'right'
      ctx.fillText(fmtReturn(asset.value), 1115, y)
      ctx.textAlign = 'left'
      ctx.strokeStyle = 'rgba(148, 163, 184, 0.14)'
      ctx.beginPath()
      ctx.moveTo(730, y + 22)
      ctx.lineTo(1115, y + 22)
      ctx.stroke()
    })
  }

  ctx.fillStyle = '#475569'
  ctx.font = '400 16px Geist, system-ui, sans-serif'
  ctx.fillText(`Generated ${new Date().toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })}`, 72, 576)
  ctx.textAlign = 'right'
  ctx.fillText('orbital.markets', 1128, 576)
  ctx.textAlign = 'left'

  const blob = await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((value) => value ? resolve(value) : reject(new Error('Unable to encode performance card')), 'image/png')
  })
  const file = new File([blob], 'orbital-return.png', { type: 'image/png' })

  if (navigator.share && navigator.canShare?.({ files: [file] })) {
    await navigator.share({
      title: performance.annualized ? 'Orbital Annualized Return' : 'Orbital Return',
      text: `${fmtReturn(performance.value)} ${performance.annualized ? 'annualized return' : 'return on deployed capital'} with Orbital`,
      files: [file],
    })
    return
  }

  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = file.name
  link.click()
  URL.revokeObjectURL(url)
}

function Tile({
  label,
  value,
  hint,
  valueClassName,
  tone = 'plain',
}: {
  label: string
  value: string
  hint?: string
  valueClassName?: string
  tone?: TileTone
}) {
  const style = TILE_TONES[tone]
  return (
    <div className={`relative overflow-hidden rounded-md border border-white/[0.07] bg-[#0b1018] px-3 py-3 transition-colors hover:border-white/[0.12] ${style.surface}`}>
      <span className={`pointer-events-none absolute inset-x-0 top-0 h-px ${style.line}`} />
      <div className="relative">
        <p className="text-[11px] text-muted-foreground">{label}</p>
        <p className={`mt-1 text-lg font-mono ${valueClassName ?? 'text-foreground'}`}>{value}</p>
        {hint && <p className="mt-0.5 text-[11px] text-muted-foreground/70">{hint}</p>}
      </div>
    </div>
  )
}

type TileTone = 'plain' | 'cyan' | 'green' | 'rose'

const TILE_TONES: Record<TileTone, { surface: string; line: string }> = {
  plain: {
    surface: '',
    line: 'bg-transparent',
  },
  cyan: {
    surface: 'bg-[radial-gradient(circle_at_90%_-25%,rgba(34,211,238,0.07),transparent_62%)]',
    line: 'bg-gradient-to-r from-transparent via-white/15 to-transparent',
  },
  green: {
    surface: 'bg-[radial-gradient(circle_at_90%_-25%,rgba(34,197,94,0.07),transparent_62%)]',
    line: 'bg-gradient-to-r from-transparent via-white/15 to-transparent',
  },
  rose: {
    surface: 'bg-[radial-gradient(circle_at_90%_-25%,rgba(244,63,94,0.07),transparent_62%)]',
    line: 'bg-gradient-to-r from-transparent via-white/15 to-transparent',
  },
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
  { label: string; color: string; dot: string; loading?: boolean }
> = {
  ready:             { label: 'Ready',           color: 'text-green-400',        dot: 'bg-green-400' },
  disconnected:      { label: 'Not connected',   color: 'text-muted-foreground', dot: 'bg-muted-foreground' },
  wallet_connected:  { label: 'Wallet only',     color: 'text-yellow-400',       dot: 'bg-yellow-400' },
  signer_missing:    { label: 'Signer missing',  color: 'text-yellow-400',       dot: 'bg-yellow-400' },
  agent_missing:     { label: 'Authorization required', color: 'text-yellow-400', dot: 'bg-yellow-400' },
  agent_authorizing: { label: 'Authorizing',     color: 'text-cyan-400',         dot: 'bg-cyan-400', loading: true },
  balance_pending:   { label: 'Pending',         color: 'text-cyan-400',         dot: 'bg-cyan-400', loading: true },
  account_stale:     { label: 'Data stale',      color: 'text-yellow-400',       dot: 'bg-yellow-400' },
  error:             { label: 'Error',           color: 'text-red-400',          dot: 'bg-red-400' },
}

const VENUE_LOGOS: Record<VenueReadiness['venue'], string> = {
  pacifica: pacificaLogo,
  hyperliquid: hlLogo,
}

const VENUE_CARD_STYLES: Record<VenueReadiness['venue'], string> = {
  pacifica: 'bg-[radial-gradient(circle_at_8%_0%,rgba(34,211,238,0.055),transparent_52%)]',
  hyperliquid: 'bg-[radial-gradient(circle_at_8%_0%,rgba(139,92,246,0.055),transparent_52%)]',
}

function VenueCard({ readiness, maskAmounts }: { readiness: VenueReadiness; maskAmounts: boolean }) {
  const view = STATUS_VIEW[readiness.status]
  // Show a real number only when we actually have one from the backend.
  // On disconnect (or before the first snapshot) equity/available are null;
  // render "--" rather than an ambiguous $0.00.
  return (
    <div className={`relative overflow-hidden rounded-md border border-white/[0.07] bg-[#0b1018] px-3 py-3 transition-colors hover:border-white/[0.12] ${VENUE_CARD_STYLES[readiness.venue]}`}>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="flex size-7 items-center justify-center rounded-md border border-border bg-white/[0.035]">
            <img src={VENUE_LOGOS[readiness.venue]} alt="" className="size-5 object-contain" />
          </span>
          <span className="text-sm font-medium text-foreground">{readiness.label}</span>
        </div>
        <span className={`text-[11px] flex items-center gap-1.5 ${view.color}`}>
          {view.loading ? (
            <span className="size-2.5 animate-spin rounded-full border border-slate-500/40 border-t-cyan-400" />
          ) : (
            <span className={`size-1.5 rounded-full ${view.dot}`} />
          )}
          {view.label}
        </span>
      </div>
      <div className="mt-2 grid grid-cols-2 gap-2 text-[12px]">
        <div>
          <p className="text-muted-foreground">Equity</p>
          <p className="font-mono text-foreground">{maskAmounts ? MASKED_VALUE : fmtUsd(readiness.equity)}</p>
        </div>
        <div>
          <p className="text-muted-foreground">Available</p>
          <p className="font-mono text-foreground">{maskAmounts ? MASKED_VALUE : fmtUsd(readiness.available)}</p>
        </div>
      </div>
      {readiness.shortAddress && (
        <p className="mt-2 text-[11px] font-mono text-muted-foreground/70">{readiness.shortAddress}</p>
      )}
    </div>
  )
}
