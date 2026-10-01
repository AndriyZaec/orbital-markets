import { useEffect, useMemo, useState } from 'react'
import { CopyIcon, DownloadIcon, EyeIcon, EyeOffIcon, Share2Icon, XIcon } from 'lucide-react'
import { useLivePositions, type LivePosition } from '@/hooks/useLivePositions'
import { useLiveActivity, type LiveActivityItem } from '@/hooks/useLiveActivity'
import type { LiveFillDetail } from '@/hooks/useLivePositionDetail'
import { useVenueReadiness, type VenueReadiness } from '@/hooks/useVenueReadiness'
import { AssetIcon } from '@/components/AssetIcon'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { isActivePositionState, isClosedPositionState, portfolioPositionCategory } from '@/lib/portfolio-position'
import { venueMetadata } from '@/lib/venue-metadata'
import {
  portfolioPerformance,
  type PortfolioPerformance,
} from '@/lib/portfolio-performance'
import { LivePositionDetail } from '@/components/LivePositionDetail'
import { ClosedPositionShareDialog } from '@/components/ClosedPositionShareDialog'

// Portfolio is the primary account/position surface for closed-beta users.

interface Props {
  onConnectWallets: () => void
  onViewPositions: () => void
  onOpenPosition: (position: LivePosition) => void
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
    case 'pending': return 'Pending'
    case 'opening': return 'Opening'
    case 'opened': return 'Opened'
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

export function Portfolio({ onConnectWallets, onViewPositions, onOpenPosition }: Props) {
  const { positions, loading: positionsLoading, error: positionsError } = useLivePositions()
  // One typed readiness layer, shared with the header and ConnectAccounts.
  const { pacifica, hyperliquid, aster, aggregate: readiness } = useVenueReadiness()
  const [privateView, setPrivateView] = useState(false)
  const [shareOpen, setShareOpen] = useState(false)
  const [sharePerformance, setSharePerformance] = useState<PortfolioPerformance | null>(null)
  const activity = useLiveActivity()
  const [selectedClosedPosition, setSelectedClosedPosition] = useState<LivePosition | null>(null)
  const [closedShare, setClosedShare] = useState<{ position: LivePosition; fills?: LiveFillDetail[] } | null>(null)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000)
    return () => window.clearInterval(timer)
  }, [])

  // Sum only venues that actually report a value. If no venue has
  // reported equity, keep the tile as "--" rather than showing $0.00.
  const equityValues = [pacifica.equity, hyperliquid.equity, aster.equity].filter(
    (v): v is number => typeof v === 'number' && Number.isFinite(v),
  )
  const availableValues = [pacifica.available, hyperliquid.available, aster.available].filter(
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

  const recentPositions = positions.filter((position) => isActivePositionState(position.state)).slice(0, 5)
  const unrealizedPerformance = useMemo(
    () => portfolioPerformance(positions.filter((position) => {
      const category = categorize(position)
      return category === 'open' || category === 'degraded'
    }), now),
    [positions, now],
  )
  const realizedPerformance = useMemo(
    () => portfolioPerformance(
      positions.filter((position) => position.state.toLowerCase() === 'closed'),
      now,
    ),
    [positions, now],
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

  const handleShare = () => {
    if (sharedPerformance.value === null) return
    setSharePerformance(sharedPerformance)
    setShareOpen(true)
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
          {sharedPerformance.value !== null && (
            <button
              type="button"
              onClick={handleShare}
              className="flex items-center gap-1.5 text-[12px] text-cyan-400 transition-colors hover:text-cyan-300"
              aria-label="Share annualized return"
            >
              <Share2Icon className="size-3.5" />
              <span className="hidden sm:inline">Share</span>
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
          !readiness.tradingReady && (
            <button
              onClick={onConnectWallets}
              className="text-[12px] text-blue-400 hover:text-blue-300 transition-colors"
            >
              Open Connect Accounts →
            </button>
          )
        }
      >
        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
          <VenueCard readiness={pacifica} maskAmounts={privateView} />
          <VenueCard readiness={hyperliquid} maskAmounts={privateView} />
          <VenueCard readiness={aster} maskAmounts={privateView} />
        </div>
      </Section>

      {/* Live positions */}
      <Section
        title="Live Positions"
        action={
          recentPositions.length > 0 && (
            <button type="button" aria-label="View all open positions" onClick={onViewPositions} className="text-[12px] text-muted-foreground hover:text-foreground">
              View all →
            </button>
          )
        }
      >
        {positionsError && <p className="text-[12px] text-red-400">Error: {positionsError}</p>}
        {!positionsError && positionsLoading && positions.length === 0 && (
          <p className="text-[12px] text-muted-foreground">Loading positions…</p>
        )}
        {!positionsLoading && recentPositions.length === 0 && (
          <p className="text-[12px] text-muted-foreground">No live positions yet.</p>
        )}
        {recentPositions.length > 0 && (
          <div className="divide-y divide-border/70 border-y border-border/70">
            {recentPositions.map((position) => {
              const category = categorize(position)
              const statusTone = category === 'degraded' ? 'text-orange-400' : position.state === 'pending' || position.state === 'closing' ? 'text-yellow-400' : 'text-green-400'
              return (
                <button
                  key={position.id}
                  type="button"
                  onClick={() => onOpenPosition(position)}
                  className="grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-1 py-3 text-left outline-none transition-colors hover:bg-white/[0.025] focus-visible:bg-white/[0.04] sm:grid-cols-[minmax(0,1fr)_120px_130px]"
                >
                  <span className="flex min-w-0 items-center gap-2.5">
                    <AssetIcon asset={position.asset} size="sm" />
                    <span className="min-w-0">
                      <span className="block truncate text-sm font-medium text-foreground">{position.asset}</span>
                      <span className={`mt-0.5 flex items-center gap-1.5 text-[11px] capitalize ${statusTone}`}><span className="size-1.5 rounded-full bg-current" />{position.state}</span>
                    </span>
                  </span>
                  <span className="hidden text-right font-mono text-xs text-muted-foreground sm:block">{privateView ? MASKED_VALUE : fmtUsd(position.notional)}</span>
                  <span className={`text-right font-mono text-xs ${position.total_pnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                    {privateView ? fmtReturn(portfolioPerformance([position], now).value) : fmtUsd(position.total_pnl)}
                  </span>
                </button>
              )
            })}
          </div>
        )}
      </Section>

      <Section title="Activity">
        {activity.loading && activity.items.length === 0 && <p className="py-3 text-[12px] text-muted-foreground">Loading activity...</p>}
        {activity.error && activity.items.length === 0 && (
          <div className="flex items-center justify-between gap-3 py-3 text-[12px]"><span className="text-red-400">{activity.error}</span><button type="button" onClick={activity.refetch} className="text-cyan-400 hover:text-cyan-300">Retry</button></div>
        )}
        {!activity.loading && !activity.error && activity.items.length === 0 && <p className="py-3 text-[12px] text-muted-foreground">No activity yet.</p>}
        {activity.items.length > 0 && (
          <div className="divide-y divide-border/70 border-y border-border/70">
            {activity.items.map((item) => (
              <ActivityRow
                key={item.id}
                item={item}
                 now={now}
                onOpenPosition={onOpenPosition}
                onOpenClosed={setSelectedClosedPosition}
                 onShare={(position) => setClosedShare({ position })}
              />
            ))}
          </div>
        )}
        {activity.nextCursor && (
          <button type="button" onClick={activity.loadMore} disabled={activity.loadingMore} className="mt-2 self-start text-[12px] text-cyan-400 hover:text-cyan-300 disabled:opacity-50">
            {activity.loadingMore ? 'Loading...' : 'Load more'}
          </button>
        )}
        {activity.error && activity.items.length > 0 && (
          <div className="mt-2 flex items-center gap-3 text-[12px]">
            <span className="text-red-400">{activity.error}</span>
            <button type="button" onClick={activity.refetch} className="text-cyan-400 hover:text-cyan-300">Refresh</button>
          </div>
        )}
      </Section>
      <PortfolioShareDialog
        open={shareOpen}
        onOpenChange={setShareOpen}
        performance={sharePerformance}
      />
      {selectedClosedPosition && (
        <LivePositionDetail
          position={selectedClosedPosition}
          onClose={() => setSelectedClosedPosition(null)}
          onShare={(position, fills) => setClosedShare({ position, fills })}
        />
      )}
      {closedShare && (
        <ClosedPositionShareDialog
          open
          onOpenChange={(open) => { if (!open) setClosedShare(null) }}
          position={closedShare.position}
          fills={closedShare.fills}
        />
      )}
    </div>
  )
}

interface ShareImage {
  blob: Blob
  file: File
}

async function createPerformanceCard(performance: PortfolioPerformance): Promise<ShareImage> {
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

  const lowerGlow = ctx.createRadialGradient(120, 610, 0, 120, 610, 520)
  lowerGlow.addColorStop(0, 'rgba(37, 99, 235, 0.14)')
  lowerGlow.addColorStop(1, 'rgba(37, 99, 235, 0)')
  ctx.fillStyle = lowerGlow
  ctx.fillRect(0, 0, canvas.width, canvas.height)

  ctx.lineWidth = 1
  for (let x = 0; x <= canvas.width; x += 80) {
    ctx.strokeStyle = x % 320 === 0
      ? 'rgba(103, 232, 249, 0.09)'
      : 'rgba(103, 232, 249, 0.055)'
    ctx.beginPath()
    ctx.moveTo(x, 0)
    ctx.lineTo(x, canvas.height)
    ctx.stroke()
  }
  for (let y = 0; y <= canvas.height; y += 70) {
    ctx.strokeStyle = y % 280 === 0
      ? 'rgba(103, 232, 249, 0.09)'
      : 'rgba(103, 232, 249, 0.055)'
    ctx.beginPath()
    ctx.moveTo(0, y)
    ctx.lineTo(canvas.width, y)
    ctx.stroke()
  }

  const glow = ctx.createRadialGradient(980, 80, 0, 980, 80, 520)
  glow.addColorStop(0, 'rgba(34, 211, 238, 0.20)')
  glow.addColorStop(1, 'rgba(34, 211, 238, 0)')
  ctx.fillStyle = glow
  ctx.fillRect(0, 0, canvas.width, canvas.height)

  drawOrbitalMark(ctx, 72, 55, 40)
  ctx.fillStyle = '#f8fafc'
  ctx.font = "650 34px 'Geist Variable', system-ui, sans-serif"
  ctx.fillText('ORBITAL MARKETS', 128, 84)

  ctx.fillStyle = '#67e8f9'
  ctx.font = '600 18px ui-monospace, SFMono-Regular, Menlo, monospace'
  ctx.fillText((performance.annualized ? 'Annualized Return' : 'Return on Deployed Capital').toUpperCase(), 72, 210)
  ctx.fillStyle = performance.value >= 0 ? '#4ade80' : '#fb7185'
  ctx.font = "700 96px 'Geist Variable', system-ui, sans-serif"
  ctx.fillText(fmtReturn(performance.value), 66, 326)

  const assets = performance.byAsset.slice(0, 3)
  if (assets.length > 0) {
    ctx.fillStyle = '#64748b'
    ctx.font = '600 15px ui-monospace, SFMono-Regular, Menlo, monospace'
    ctx.fillText('BY ASSET', 730, 215)

    assets.forEach((asset, index) => {
      const y = 265 + index * 62
      ctx.fillStyle = '#e2e8f0'
      ctx.font = "600 22px 'Geist Variable', system-ui, sans-serif"
      ctx.fillText(asset.asset, 730, y)
      ctx.fillStyle = asset.value >= 0 ? '#4ade80' : '#fb7185'
      ctx.font = '600 20px ui-monospace, SFMono-Regular, Menlo, monospace'
      ctx.textAlign = 'right'
      ctx.fillText(fmtReturn(asset.value), 1115, y)
      ctx.textAlign = 'left'
    })
  }

  ctx.fillStyle = '#475569'
  ctx.font = "400 16px 'Geist Variable', system-ui, sans-serif"
  ctx.fillText(`Generated ${new Date().toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })}`, 72, 576)
  ctx.textAlign = 'right'
  ctx.fillText('orbital.markets', 1128, 576)
  ctx.textAlign = 'left'

  const blob = await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((value) => value ? resolve(value) : reject(new Error('Unable to encode performance card')), 'image/png')
  })
  const file = new File([blob], 'orbital-return.png', { type: 'image/png' })
  return { blob, file }
}

function drawOrbitalMark(ctx: CanvasRenderingContext2D, x: number, y: number, size: number): void {
  ctx.save()
  ctx.translate(x + size / 2, y + size / 2)
  ctx.strokeStyle = 'rgba(148, 163, 184, 0.72)'
  ctx.lineWidth = 1.5
  ctx.rotate(-0.38)
  ctx.beginPath()
  ctx.ellipse(0, 0, size / 2, size / 4, 0, 0, Math.PI * 2)
  ctx.stroke()
  ctx.rotate(0.95)
  ctx.beginPath()
  ctx.ellipse(0, 0, size / 3.2, size / 5, 0, 0, Math.PI * 2)
  ctx.stroke()
  ctx.fillStyle = '#22d3ee'
  ctx.shadowColor = 'rgba(34, 211, 238, 0.8)'
  ctx.shadowBlur = 10
  ctx.beginPath()
  ctx.arc(0, 0, 5, 0, Math.PI * 2)
  ctx.fill()
  ctx.restore()
}

function PortfolioShareDialog({
  open,
  onOpenChange,
  performance,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  performance: PortfolioPerformance | null
}) {
  const [image, setImage] = useState<(ShareImage & { url: string }) | null>(null)
  const [status, setStatus] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open || !performance || performance.value === null) return
    let cancelled = false
    let imageUrl: string | null = null
    setImage(null)
    setStatus(null)
    setError(null)

    createPerformanceCard(performance).then((result) => {
      imageUrl = URL.createObjectURL(result.blob)
      if (cancelled) {
        URL.revokeObjectURL(imageUrl)
        return
      }
      setImage({ ...result, url: imageUrl })
    }).catch(() => {
      if (!cancelled) setError('Unable to prepare the performance card.')
    })

    return () => {
      cancelled = true
      if (imageUrl) URL.revokeObjectURL(imageUrl)
    }
  }, [open, performance])

  const canNativeShare = !!image
    && typeof navigator.share === 'function'
    && !!navigator.canShare?.({ files: [image.file] })

  const nativeShare = async () => {
    if (!image || !performance || performance.value === null || !canNativeShare) return
    setStatus(null)
    setError(null)
    try {
      await navigator.share({
        title: performance.annualized ? 'Orbital Annualized Return' : 'Orbital Return',
        text: `${fmtReturn(performance.value)} ${performance.annualized ? 'annualized return' : 'return on deployed capital'} with Orbital`,
        files: [image.file],
      })
      setStatus('Shared successfully.')
    } catch (shareError) {
      if (!(shareError instanceof DOMException && shareError.name === 'AbortError')) {
        setError('System sharing is unavailable. Copy or download the image instead.')
      }
    }
  }

  const copyImage = async () => {
    if (!image) return
    setStatus(null)
    setError(null)
    try {
      if (!navigator.clipboard?.write || typeof ClipboardItem === 'undefined') throw new Error('Unsupported')
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': image.blob })])
      setStatus('Image copied to clipboard.')
    } catch {
      setError('This browser cannot copy images. Download the PNG instead.')
    }
  }

  const downloadImage = () => {
    if (!image) return
    const link = document.createElement('a')
    link.href = image.url
    link.download = image.file.name
    link.click()
    setError(null)
    setStatus('PNG downloaded.')
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="bg-[#080d15] text-slate-100 sm:max-w-2xl" showCloseButton={false}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-[0.14em] text-slate-100">
            <Share2Icon className="size-4 text-cyan-400" />
            Share performance
          </DialogTitle>
        </DialogHeader>
        <DialogClose
          render={(
            <Button
              variant="ghost"
              size="icon-sm"
              className="absolute top-2 right-2 text-slate-300 hover:bg-white/10 hover:text-white"
            />
          )}
        >
          <XIcon />
          <span className="sr-only">Close</span>
        </DialogClose>

        {image ? (
          <img
            src={image.url}
            alt="Orbital performance share card preview"
            className="w-full rounded-lg ring-1 ring-foreground/10"
          />
        ) : (
          <div className="flex aspect-[40/21] items-center justify-center rounded-lg bg-muted/40 text-sm text-muted-foreground">
            {error ?? 'Preparing preview…'}
          </div>
        )}

        {error && image && <p role="status" className="text-xs text-destructive">{error}</p>}
        {status && image && <p role="status" className="text-xs text-muted-foreground">{status}</p>}

        <DialogFooter className="border-white/[0.07] bg-[#080d15] sm:justify-between">
          <div className="flex flex-wrap gap-2">
            <Button
              variant="ghost"
              className="bg-white/[0.04] text-slate-300 hover:bg-white/[0.08] hover:text-white"
              onClick={copyImage}
              disabled={!image}
            >
              <CopyIcon data-icon="inline-start" />
              Copy image
            </Button>
            <Button
              variant="ghost"
              className="bg-white/[0.04] text-slate-300 hover:bg-white/[0.08] hover:text-white"
              onClick={downloadImage}
              disabled={!image}
            >
              <DownloadIcon data-icon="inline-start" />
              Download PNG
            </Button>
          </div>
          {canNativeShare && (
            <Button
              variant="ghost"
              className="bg-cyan-500/10 text-cyan-300 hover:bg-cyan-500/15 hover:text-cyan-200"
              onClick={nativeShare}
              disabled={!image}
            >
              <Share2Icon data-icon="inline-start" />
              Share image
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ActivityRow({ item, now, onOpenPosition, onOpenClosed, onShare }: {
  item: LiveActivityItem
  now: number
  onOpenPosition: (position: LivePosition) => void
  onOpenClosed: (position: LivePosition) => void
  onShare: (position: LivePosition) => void
}) {
  const position = item.position
  const active = isActivePositionState(position.state)
  const closed = isClosedPositionState(position.state)
  const performance = portfolioPerformance([position], now)
  const metric = performance.value === null ? '--' : fmtReturn(performance.value)
  const activate = () => active ? onOpenPosition(position) : onOpenClosed(position)
  const label = actionLabel(item.type || position.state)
  const statusTone = closed ? 'text-slate-400' : position.state === 'degraded' ? 'text-orange-400' : 'text-cyan-400'

  return (
    <div
      className="group grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-1 py-3 transition-colors hover:bg-white/[0.025] sm:grid-cols-[minmax(0,1fr)_minmax(150px,0.7fr)_130px_auto]"
    >
      <button
        type="button"
        aria-label={`${label} ${position.asset} position`}
        onClick={activate}
        className="col-span-2 grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 text-left outline-none focus-visible:ring-1 focus-visible:ring-cyan-400/60 sm:col-span-3 sm:grid-cols-[minmax(0,1fr)_minmax(150px,0.7fr)_130px]"
      >
        <span className="flex min-w-0 items-center gap-2.5">
          <AssetIcon asset={position.asset} size="sm" />
          <span className="min-w-0">
            <span className="block truncate text-sm font-medium text-foreground">{position.asset}</span>
            <span className="mt-0.5 block truncate text-[11px] capitalize text-muted-foreground">{position.venue_a} / {position.venue_b}</span>
            <span className={`mt-1 flex items-center gap-1.5 text-[10px] sm:hidden ${statusTone}`}><span className="size-1.5 rounded-full bg-current" />{label} · {fmtRelative(item.at, now)}</span>
          </span>
        </span>
        <span className="hidden min-w-0 sm:block">
          <span className={`flex items-center gap-1.5 text-xs ${statusTone}`}><span className="size-1.5 rounded-full bg-current" />{label}</span>
          <span className="mt-0.5 block text-[10px] text-muted-foreground">{fmtRelative(item.at, now)}</span>
        </span>
        <span className="text-right">
          <span className={`block font-mono text-xs ${position.total_pnl < 0 ? 'text-red-400' : 'text-green-400'}`}>{metric}</span>
          <span className="mt-0.5 block text-[10px] text-muted-foreground">{performance.annualized ? 'APR' : 'ROI'}</span>
        </span>
      </button>
      {closed ? (
        <button
          type="button"
          aria-label={`Share ${position.asset} closed position`}
          onClick={() => onShare(position)}
          className="col-start-2 row-start-2 flex items-center gap-1 justify-self-end text-[11px] text-muted-foreground transition-colors hover:text-cyan-300 sm:col-start-4 sm:row-start-1"
        >
          <Share2Icon className="size-3.5" /> Share
        </button>
      ) : (
        <span className="hidden sm:block" aria-hidden="true" />
      )}
    </div>
  )
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
        <p className={`mt-1 text-lg font-mono ${valueClassName || 'text-foreground'}`}>{value}</p>
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
    <section aria-label={title} className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <h2 className="text-[13px] font-semibold text-foreground">{title}</h2>
        {action}
      </div>
      {children}
    </section>
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
  unavailable:       { label: 'Unavailable',     color: 'text-red-400',          dot: 'bg-red-400' },
  error:             { label: 'Error',           color: 'text-red-400',          dot: 'bg-red-400' },
}

const VENUE_CARD_STYLES: Record<VenueReadiness['venue'], string> = {
  pacifica: 'bg-[radial-gradient(circle_at_8%_0%,rgba(34,211,238,0.055),transparent_52%)]',
  hyperliquid: 'bg-[radial-gradient(circle_at_8%_0%,rgba(139,92,246,0.055),transparent_52%)]',
  aster: 'bg-[radial-gradient(circle_at_8%_0%,rgba(245,158,11,0.055),transparent_52%)]',
}

function VenueCard({ readiness, maskAmounts }: { readiness: VenueReadiness; maskAmounts: boolean }) {
  const view = STATUS_VIEW[readiness.status]
  const logo = venueMetadata(readiness.venue).logo
  // Show a real number only when we actually have one from the backend.
  // On disconnect (or before the first snapshot) equity/available are null;
  // render "--" rather than an ambiguous $0.00.
  return (
    <div className={`relative overflow-hidden rounded-md border border-white/[0.07] bg-[#0b1018] px-3 py-3 transition-colors hover:border-white/[0.12] ${VENUE_CARD_STYLES[readiness.venue]}`}>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="flex size-7 items-center justify-center rounded-md border border-border bg-white/[0.035]">
            {logo ? <img src={logo} alt="" className="size-5 object-contain" /> : readiness.label[0]}
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
