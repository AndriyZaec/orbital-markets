import { useId, useState, type PointerEvent } from 'react'
import type { LivePosition } from '@/hooks/useLivePositions'
import { usePositionPriceHistory } from '@/hooks/usePositionPriceHistory'
import { positionPricePnLSeries, type PositionPricePnLContext } from '@/lib/position-price-pnl'
import { paddedChartDomain } from '@/lib/funding-chart'
import { monotonePath } from '@/lib/chart-path'
import { formatSignedUsdPnL } from '@/lib/pnl-format'
import { venueMetadata } from '@/lib/venue-metadata'
import { cn } from '@/lib/utils'

export function PositionPricePnLChart({ position, context, accounts }: {
  position: LivePosition
  context?: PositionPricePnLContext
  accounts?: Record<string, string>
}) {
  const history = usePositionPriceHistory(position.id, accounts, context?.available === true && position.state === 'open', context?.available ? context.valuation_key : '')
  if (!context?.available || context.latest.price_pnl !== position.price_pnl || position.state !== 'open') {
    return <div className="flex h-[272px] items-center justify-center text-xs text-muted-foreground">
      {context && !context.available ? context.reason : 'Waiting for an updated Price PnL snapshot.'}
    </div>
  }
  const points = positionPricePnLSeries(history.data?.points ?? [], context.latest, context.opened_at, history.data?.start)
  const long = venueMetadata(context.long_venue)
  const short = venueMetadata(context.short_venue)
  return <>
    <div className="mb-3 grid max-w-xl grid-cols-3 gap-3">
      <PnLMetric label={`Long · ${long.label}`} value={context.latest.long_pnl} />
      <PnLMetric label={`Short · ${short.label}`} value={context.latest.short_pnl} />
      <PnLMetric label="Price PnL" value={position.price_pnl} />
    </div>
    {history.loading ? <div className="flex h-[210px] items-center justify-center text-xs text-muted-foreground">Loading Price PnL history...</div>
      : history.error ? <div className="flex h-[210px] items-center justify-center text-xs text-muted-foreground">{history.error}</div>
        : <PricePnLPlot points={points} openedAt={context.opened_at} longLabel={long.label} shortLabel={short.label} />}
  </>
}

function PnLMetric({ label, value }: { label: string; value: number | null }) {
  return <div>
    <p className="mb-0.5 text-[10px] text-muted-foreground">{label}</p>
    <p className={cn('font-mono text-sm font-medium', value === null || value === 0 ? 'text-foreground' : value > 0 ? 'text-chart-3' : 'text-destructive')}>
      {value === null ? '—' : formatSignedUsdPnL(value)}
    </p>
  </div>
}

function PricePnLPlot({ points, openedAt, longLabel, shortLabel }: {
  points: ReturnType<typeof positionPricePnLSeries>
  openedAt: string
  longLabel: string
  shortLabel: string
}) {
  const [hoverTime, setHoverTime] = useState<number | null>(null)
  const clipId = useId().replace(/:/g, '')
  const samples = points.map((point) => ({ time: Date.parse(point.t), segment: point.segment,
    value: typeof point.price_pnl === 'number' && Number.isFinite(point.price_pnl) &&
      typeof point.long_pnl === 'number' && Number.isFinite(point.long_pnl) &&
      typeof point.short_pnl === 'number' && Number.isFinite(point.short_pnl) ? point.price_pnl : null,
    long: point.long_pnl, short: point.short_pnl,
  }))
  const segments: (typeof samples)[] = []
  for (const sample of samples) {
    if (sample.value === null) {
      if (segments[segments.length - 1]?.length) segments.push([])
      continue
    }
    let segment = segments[segments.length - 1]
    const previous = segment?.[segment.length - 1]
    if (!segment || (previous && (sample.segment !== previous.segment || sample.time - previous.time > 15 * 60_000))) {
      segment = []
      segments.push(segment)
    }
    segment.push(sample)
  }
  const visible = segments.flat().filter((sample): sample is typeof sample & { value: number } => sample.value !== null)
  if (visible.length === 0) return <div className="flex h-[210px] items-center justify-center text-xs text-muted-foreground">No Price PnL samples in the last 24H.</div>
  const latest = visible[visible.length - 1]
  const width = 1100, height = 210, left = 90, right = 24, top = 12, bottom = 178
  const entryTime = Date.parse(openedAt)
  const inWindowEntry = entryTime >= latest.time - 86_400_000 && entryTime <= latest.time
  const firstTime = inWindowEntry ? Math.min(entryTime, visible[0].time) : visible[0].time
  const minTime = firstTime - (firstTime === latest.time ? 30_000 : 0)
  const maxTime = latest.time + (firstTime === latest.time ? 30_000 : 0)
  const x = (time: number) => left + (time - minTime) / (maxTime - minTime) * (width - left - right)
  const domain = paddedChartDomain([0, ...visible.map((point) => point.value)], 0.02)
  const y = (value: number) => bottom - (value - domain.min) / (domain.max - domain.min) * (bottom - top)
  const zeroY = y(0)
  const path = segments.filter((segment) => segment.length).map((segment) => monotonePath(segment.map((point) => ({ x: x(point.time), y: y(point.value!) })))).join(' ')
  const hoverSegment = hoverTime === null ? undefined : segments.find((segment) => segment.length &&
    (segment.length === 1 ? Math.abs(segment[0].time - hoverTime) <= 30_000 : hoverTime >= segment[0].time && hoverTime <= segment[segment.length - 1].time))
  const hovered = hoverSegment && hoverTime !== null ? hoverSegment.reduce((nearest, point) =>
    Math.abs(point.time - hoverTime) < Math.abs(nearest.time - hoverTime) ? point : nearest) : null
  const formatTime = (time: number) => new Date(time).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  const handlePointer = (event: PointerEvent<SVGSVGElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    if (rect.width <= 0) return
    const svgX = (event.clientX - rect.left) / rect.width * width
    setHoverTime(svgX < left || svgX > width - right ? null : minTime + (svgX - left) / (width - left - right) * (maxTime - minTime))
  }
  return <>
    <div className="mb-1 h-5 truncate font-mono text-[11px] text-muted-foreground">
      {hovered ? `${formatTime(hovered.time)} · Long ${longLabel} ${formatSignedUsdPnL(hovered.long!)} · Short ${shortLabel} ${formatSignedUsdPnL(hovered.short!)} · Price PnL ${formatSignedUsdPnL(hovered.value!)}`
        : `Latest snapshot ${formatTime(latest.time)}`}
    </div>
    <svg viewBox={`0 0 ${width} ${height}`} className="w-full touch-none" role="img" aria-label="Position Price PnL history" onPointerMove={handlePointer} onPointerLeave={() => setHoverTime(null)}>
      <defs>
        <clipPath id={`${clipId}-positive`}><rect x={left} y={top} width={width - left - right} height={Math.max(0, zeroY - top - 0.5)} /></clipPath>
        <clipPath id={`${clipId}-negative`}><rect x={left} y={zeroY + 0.5} width={width - left - right} height={Math.max(0, bottom - zeroY - 0.5)} /></clipPath>
      </defs>
      {Array.from({ length: 5 }, (_, i) => {
        const value = domain.min + (domain.max - domain.min) * i / 4
        return <g key={i}><line x1={left} y1={y(value)} x2={width - right} y2={y(value)} className="stroke-border" />
          <text x={left - 8} y={y(value) + 3} className="fill-muted-foreground" fontSize="9" fontFamily="monospace" textAnchor="end">{formatSignedUsdPnL(value)}</text></g>
      })}
      <line x1={left} y1={zeroY} x2={width - right} y2={zeroY} className="stroke-muted-foreground" strokeDasharray="3 4" />
      <path d={path} fill="none" className="stroke-muted-foreground" strokeWidth="1.8" vectorEffect="non-scaling-stroke" aria-label="Price PnL curve" />
      <path d={path} fill="none" className="stroke-chart-3" strokeWidth="1.8" vectorEffect="non-scaling-stroke" clipPath={`url(#${clipId}-positive)`} />
      <path d={path} fill="none" className="stroke-destructive" strokeWidth="1.8" vectorEffect="non-scaling-stroke" clipPath={`url(#${clipId}-negative)`} />
      <circle cx={x(latest.time)} cy={y(latest.value)} r="2.5" className={cn(latest.value > 0 ? 'fill-chart-3' : latest.value < 0 ? 'fill-destructive' : 'fill-foreground')} aria-label="Current Price PnL" data-value={latest.value} />
      {inWindowEntry && <g aria-label="Position entry time"><line x1={x(entryTime)} y1={top} x2={x(entryTime)} y2={bottom} className="stroke-muted-foreground" strokeDasharray="2 4" /><text x={x(entryTime) + 4} y={top + 8} className="fill-muted-foreground" fontSize="9">Entry</text></g>}
      {hovered && <g pointerEvents="none" aria-label="PnL observation" data-time={hovered.time}>
        <line x1={x(hovered.time)} y1={top} x2={x(hovered.time)} y2={bottom} className="stroke-muted-foreground" strokeDasharray="2 3" />
        <circle cx={x(hovered.time)} cy={y(hovered.value!)} r="3.5" className="fill-foreground stroke-background" />
      </g>}
      {Array.from({ length: 6 }, (_, i) => {
        const time = minTime + (maxTime - minTime) * i / 5
        return <text key={i} x={x(time)} y={height - 5} className="fill-muted-foreground" fontSize="9" fontFamily="monospace" textAnchor={i === 0 ? 'start' : i === 5 ? 'end' : 'middle'}>{formatTime(time)}</text>
      })}
    </svg>
    <p className="mt-2 text-[10px] text-muted-foreground">Price PnL · Mark Prices</p>
  </>
}
