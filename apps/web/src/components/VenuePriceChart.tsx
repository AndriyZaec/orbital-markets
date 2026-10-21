import { useState, type PointerEvent } from 'react'
import type { HistoryPoint, HistoryWindow } from '@/hooks/useHistory'
import { paddedChartDomain, type FundingDirection } from '@/lib/funding-chart'
import { monotonePath } from '@/lib/chart-path'
import { bucketSpreadSamples } from '@/lib/price-spread-chart'
import { venueMetadata } from '@/lib/venue-metadata'

function formatPrice(value: number) {
  const digits = value >= 1 || value <= 0 ? 4 : Math.min(16, Math.ceil(-Math.log10(value)) + 4)
  return '$' + value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: digits })
}

function formatBasis(value: number | null) {
  return value === null ? '—' : `${value > 0 ? '+' : ''}${(value * 100).toFixed(4)}%`
}

export function VenuePriceChart({ data, direction, venueA, venueB, window }: {
  data: HistoryPoint[]
  direction: FundingDirection
  venueA: string
  venueB: string
  window?: HistoryWindow
}) {
  const [hoverTime, setHoverTime] = useState<number | null>(null)
  const isLongA = direction === 'long_a_short_b'
  const longVenue = venueMetadata(isLongA ? venueA : venueB)
  const shortVenue = venueMetadata(isLongA ? venueB : venueA)
  const start = window ? Date.parse(window.start) : NaN
  const end = window ? Date.parse(window.end) : NaN
  const ordered = data.map((point) => {
    const a = point.mark_price_a
    const b = point.mark_price_b
    const valid = typeof a === 'number' && Number.isFinite(a) && a > 0
      && typeof b === 'number' && Number.isFinite(b) && b > 0
    return {
      time: Date.parse(point.t),
      value: valid ? (isLongA ? a : b) : null,
      shortPrice: valid ? (isLongA ? b : a) : NaN,
      basis: typeof point.price_spread === 'number' && Number.isFinite(point.price_spread) ? point.price_spread * (isLongA ? 1 : -1) : null,
    }
  }).filter((point) => Number.isFinite(point.time)
    && (!Number.isFinite(start) || point.time >= start)
    && (!Number.isFinite(end) || point.time <= end))
    .toSorted((a, b) => a.time - b.time)
  // Duplicate timestamps cannot be distinct curve knots; keep the latest row.
  const samples = ordered.filter((point, index) => point.time !== ordered[index + 1]?.time)
  const segments = bucketSpreadSamples(samples)
  const plotted = segments.flat()
  if (plotted.length === 0) {
    return <div className="flex h-[272px] items-center justify-center text-xs text-muted-foreground">No paired price history for this pair.</div>
  }

  const latest = plotted[plotted.length - 1]
  // Hover uses ONLY the exact observations used as knots by both curves.
  // Snap across a segment, but never bridge a missing-data gap.
  const hoverSegment = hoverTime === null ? undefined : segments.find((segment) =>
    segment.length === 1 ? Math.abs(segment[0].time - hoverTime) <= 30_000
      : hoverTime >= segment[0].time && hoverTime <= segment[segment.length - 1].time)
  const hovered = hoverSegment && hoverTime !== null ? hoverSegment.reduce((nearest, point) =>
    Math.abs(point.time - hoverTime) < Math.abs(nearest.time - hoverTime) ? point : nearest) : null
  const width = 1100
  const height = 210
  const left = 100
  const right = 24
  const top = 12
  const bottom = 178
  const firstTime = plotted[0].time
  const lastTime = latest.time
  const minTime = firstTime - (firstTime === lastTime ? 30_000 : 0)
  const maxTime = lastTime + (firstTime === lastTime ? 30_000 : 0)
  const plotRight = width - right
  const x = (time: number) => left + (time - minTime) / (maxTime - minTime) * (plotRight - left)
  const values = plotted.flatMap((point) => [point.value, point.shortPrice])
  const padded = paddedChartDomain(values, Math.max(latest.value * 0.00002, 1e-8))
  const domain = { min: Math.max(0, padded.min), max: padded.max }
  const y = (price: number) => bottom - (price - domain.min) / (domain.max - domain.min) * (bottom - top)
  const longPath = segments.map((segment) => monotonePath(segment.map((point) => ({ x: x(point.time), y: y(point.value) })))).join(' ')
  const shortPath = segments.map((segment) => monotonePath(segment.map((point) => ({ x: x(point.time), y: y(point.shortPrice) })))).join(' ')
  const formatTime = (time: number) => new Date(time).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  const longY = y(latest.value)
  const shortY = y(latest.shortPrice)
  const handlePointer = (event: PointerEvent<SVGSVGElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    if (rect.width <= 0) return
    const svgX = (event.clientX - rect.left) / rect.width * width
    setHoverTime(svgX < left || svgX > plotRight ? null
      : minTime + (svgX - left) / (plotRight - left) * (maxTime - minTime))
  }

  return (
    <>
      <div className="mb-3 grid max-w-xl grid-cols-3 gap-3">
        <PriceMetric label={`Long · ${longVenue.label}`} value={formatPrice(latest.value)} color={longVenue.color} />
        <PriceMetric label={`Short · ${shortVenue.label}`} value={formatPrice(latest.shortPrice)} color={shortVenue.color} />
        <PriceMetric label="Price basis" value={formatBasis(latest.basis)} />
      </div>
      <div className="mb-1 h-5 truncate font-mono text-[11px] text-muted-foreground">
        {hovered ? `${formatTime(hovered.time)} · ${longVenue.label} ${formatPrice(hovered.value)} · ${shortVenue.label} ${formatPrice(hovered.shortPrice)} · Basis ${formatBasis(hovered.basis)}`
          : `Latest snapshot ${formatTime(latest.time)}`}
      </div>
      <svg viewBox={`0 0 ${width} ${height}`} className="w-full touch-none" role="img" aria-label="Venue price history" onPointerMove={handlePointer} onPointerLeave={() => setHoverTime(null)}>
        {Array.from({ length: 5 }, (_, index) => {
          const price = domain.min + (domain.max - domain.min) * index / 4
          return <g key={index}>
            <line x1={left} y1={y(price)} x2={plotRight} y2={y(price)} className="stroke-border" />
            <text x={left - 8} y={y(price) + 3} className="fill-muted-foreground" fontSize="9" fontFamily="monospace" textAnchor="end">{formatPrice(price)}</text>
          </g>
        })}
        <path d={longPath} fill="none" stroke={longVenue.color} strokeWidth="1.8" vectorEffect="non-scaling-stroke" aria-label={`${longVenue.label} Long price curve`} />
        <path d={shortPath} fill="none" stroke={shortVenue.color} strokeWidth="1.8" vectorEffect="non-scaling-stroke" aria-label={`${shortVenue.label} Short price curve`} />
        <circle cx={x(latest.time)} cy={longY} r="2.5" fill={longVenue.color} />
        <circle cx={x(latest.time)} cy={shortY} r="2.5" fill={shortVenue.color} />
        {hovered && <g pointerEvents="none" aria-label="Price observation" data-time={hovered.time}>
          <line x1={x(hovered.time)} y1={top} x2={x(hovered.time)} y2={bottom} className="stroke-muted-foreground" strokeDasharray="2 3" />
          <circle cx={x(hovered.time)} cy={y(hovered.value)} r="3.5" fill={longVenue.color} className="stroke-background" aria-label="Long price marker" />
          <circle cx={x(hovered.time)} cy={y(hovered.shortPrice)} r="3.5" fill={shortVenue.color} className="stroke-background" aria-label="Short price marker" />
        </g>}
        {Array.from({ length: 6 }, (_, index) => {
          const time = minTime + (maxTime - minTime) * index / 5
          return <text key={index} x={x(time)} y={height - 5} className="fill-muted-foreground" fontSize="9" fontFamily="monospace" textAnchor={index === 0 ? 'start' : index === 5 ? 'end' : 'middle'}>{formatTime(time)}</text>
        })}
      </svg>
      <div className="mt-2 flex flex-wrap items-center gap-x-5 gap-y-1 text-[10px] text-muted-foreground">
        <span className="flex items-center gap-1.5"><span className="h-px w-3" style={{ backgroundColor: longVenue.color }} />{longVenue.label} · Long</span>
        <span className="flex items-center gap-1.5"><span className="h-px w-3" style={{ backgroundColor: shortVenue.color }} />{shortVenue.label} · Short</span>
        <span className="ml-auto">Mark Prices</span>
      </div>
    </>
  )
}

function PriceMetric({ label, value, color }: { label: string; value: string; color?: string }) {
  return <div><p className="mb-0.5 text-[10px] text-muted-foreground">{label}</p><p className="font-mono text-sm font-medium text-foreground" style={{ color }}>{value}</p></div>
}
