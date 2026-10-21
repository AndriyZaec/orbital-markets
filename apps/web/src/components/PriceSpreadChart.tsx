import { useState, type PointerEvent } from 'react'
import type { HistoryPoint, HistoryWindow } from '@/hooks/useHistory'
import { paddedChartDomain, type FundingDirection } from '@/lib/funding-chart'
import { monotonePath } from '@/lib/chart-path'
import { bucketSpreadSamples } from '@/lib/price-spread-chart'

export interface SpreadEntry {
  value: number
  time?: string
}

function formatSpread(value: number) {
  return `${value > 0 ? '+' : ''}${(value * 100).toFixed(4)}%`
}

function formatPrice(value: number | null) {
  if (value === null) return '—'
  return '$' + value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: value >= 1 ? 4 : 8 })
}

function validPrice(value: number | null | undefined) {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null
}

export function PriceSpreadChart({ data, direction, directionLabel, entry, window, showPremiumSummary = !entry, longLabel, shortLabel }: {
  data: HistoryPoint[]
  direction: FundingDirection
  directionLabel: string
  entry?: SpreadEntry
  window?: HistoryWindow
  showPremiumSummary?: boolean
  longLabel?: string
  shortLabel?: string
}) {
  const [hoverTime, setHoverTime] = useState<number | null>(null)
  const sign = direction === 'long_a_short_b' ? 1 : -1
  const windowStart = window ? Date.parse(window.start) : NaN
  const windowEnd = window ? Date.parse(window.end) : NaN
  const samples = data.map((point) => ({
    time: Date.parse(point.t),
    value: typeof point.price_spread === 'number' && Number.isFinite(point.price_spread) ? point.price_spread * sign : null,
    priceA: validPrice(point.mark_price_a),
    priceB: validPrice(point.mark_price_b),
  })).filter((point) => Number.isFinite(point.time)
    && (!Number.isFinite(windowStart) || point.time >= windowStart)
    && (!Number.isFinite(windowEnd) || point.time <= windowEnd))
    .toSorted((a, b) => a.time - b.time)
  const valid = samples.filter((point): point is typeof point & { value: number } => point.value !== null)
  if (valid.length === 0) {
    return <div className="flex h-[272px] items-center justify-center text-xs text-muted-foreground">No price spread history for this period.</div>
  }

  const entryValue = entry && Number.isFinite(entry.value) ? entry.value : null
  const latestSample = valid[valid.length - 1]
  const latest = latestSample.value
  const entryTime = entry?.time ? Date.parse(entry.time) : NaN
  const changeSinceEntry = entryValue !== null && Number.isFinite(entryTime) && latestSample.time >= entryTime ? latest - entryValue : null
  const hovered = hoverTime === null ? null : valid.reduce((nearest, point) => Math.abs(point.time - hoverTime) < Math.abs(nearest.time - hoverTime) ? point : nearest)
  const width = 1100
  const height = 210
  const left = 78
  const right = 24
  const top = 12
  const bottom = 178
  const dataStart = valid[0].time
  const dataEnd = latestSample.time
  const minTime = showPremiumSummary ? dataStart - (dataStart === dataEnd ? 30_000 : 0) : Number.isFinite(windowStart) ? windowStart : samples[0].time
  const maxTime = showPremiumSummary ? dataEnd + (dataStart === dataEnd ? 30_000 : 0) : Number.isFinite(windowEnd) ? windowEnd : samples[samples.length - 1].time
  const timeSpan = Math.max(1, maxTime - minTime)
  const x = (time: number) => left + (time - minTime) / timeSpan * (width - left - right)
  const values = valid.map((point) => point.value)
  const domain = paddedChartDomain([...values, 0, ...(entryValue === null ? [] : [entryValue])], 0.0001)
  const extent = Math.max(Math.abs(domain.min), Math.abs(domain.max))
  const { min, max } = showPremiumSummary ? { min: -extent, max: extent } : domain
  const y = (value: number) => bottom - (value - min) / (max - min) * (bottom - top)
  const segments = bucketSpreadSamples(samples)
  const path = segments.map((segment) => monotonePath(segment.map((point) => ({ x: x(point.time), y: y(point.value) })))).join(' ')
  const showEntryTime = entryValue !== null && entryTime >= minTime && entryTime <= maxTime
  const formatTime = (time: number) => new Date(time).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  const handlePointer = (event: PointerEvent<SVGSVGElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    if (rect.width <= 0) return
    const svgX = (event.clientX - rect.left) / rect.width * width
    setHoverTime(minTime + (svgX - left) / (width - left - right) * timeSpan)
  }
  const longPrice = sign === 1 ? latestSample.priceA : latestSample.priceB
  const shortPrice = sign === 1 ? latestSample.priceB : latestSample.priceA
  const premiumLabel = latest === 0 ? 'Long and short are equally priced' : `Short is ${(Math.abs(latest) * 100).toFixed(4)}% ${latest > 0 ? 'more expensive' : 'cheaper'}`

  return (
    <>
      {showPremiumSummary ? <div className="mb-3 flex flex-col gap-2">
        <div>
          <p className="mb-0.5 text-[10px] text-muted-foreground">Short premium</p>
          <p className="text-sm font-medium text-foreground">{premiumLabel}</p>
        </div>
        <div className="flex flex-wrap gap-x-6 gap-y-2">
          <SpreadMetric label={longLabel ? `Long · ${longLabel}` : 'Long'} value={formatPrice(longPrice)} />
          <SpreadMetric label={shortLabel ? `Short · ${shortLabel}` : 'Short'} value={formatPrice(shortPrice)} />
          <SpreadMetric label="Latest snapshot" value={new Date(latestSample.time).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })} />
        </div>
      </div> : <div className="mb-3 grid max-w-xl grid-cols-3 gap-3">
        <SpreadMetric label="Latest spread" value={formatSpread(latest)} />
        <SpreadMetric label={entryValue === null ? 'Period low' : 'Entry spread'} value={formatSpread(entryValue ?? Math.min(...values))} />
        <SpreadMetric label={entryValue === null ? 'Period high' : 'Change since entry'} value={entryValue === null ? formatSpread(Math.max(...values)) : changeSinceEntry === null ? '—' : `${changeSinceEntry > 0 ? '+' : ''}${(changeSinceEntry * 100).toFixed(4)} pp`} />
      </div>}
      <div className="mb-1 h-5 truncate font-mono text-[11px] text-muted-foreground">
        {hovered ? `${new Date(hovered.time).toLocaleString()} · Long ${formatPrice(sign === 1 ? hovered.priceA : hovered.priceB)} · Short ${formatPrice(sign === 1 ? hovered.priceB : hovered.priceA)} · Premium ${formatSpread(hovered.value)}` : directionLabel}
      </div>
      <svg viewBox={`0 0 ${width} ${height}`} className="w-full touch-none" role="img" aria-label="Price spread history between short and long venues" onPointerMove={handlePointer} onPointerLeave={() => setHoverTime(null)}>
        {Array.from({ length: 5 }, (_, index) => {
          const tick = min + (max - min) * index / 4
          return (
            <g key={index}>
              <line x1={left} y1={y(tick)} x2={width - right} y2={y(tick)} className="stroke-border" />
              <text x={left - 8} y={y(tick) + 3} className="fill-muted-foreground" fontSize="9" fontFamily="monospace" textAnchor="end">{formatSpread(tick)}</text>
            </g>
          )
        })}
        <line x1={left} y1={y(0)} x2={width - right} y2={y(0)} className="stroke-muted-foreground" strokeDasharray="3 4" />
        {segments.map((segment, index) => {
          const band = [...segment.map((point) => ({ x: x(point.time), y: y(point.high) })), ...segment.toReversed().map((point) => ({ x: x(point.time), y: y(point.low) }))]
          return <path key={index} d={band.map((point, i) => `${i === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' ') + ' Z'} className="fill-primary" opacity="0.08" aria-label="Observed premium range" />
        })}
        <path d={path} fill="none" className="stroke-primary" strokeWidth="1.8" vectorEffect="non-scaling-stroke" aria-label="Premium trend" />
        <circle cx={x(latestSample.time)} cy={y(latest)} r="2.5" className="fill-primary" aria-label="Latest premium" />
        {entryValue !== null && (
          <g aria-label="Entry spread level">
            <line x1={left} y1={y(entryValue)} x2={width - right} y2={y(entryValue)} className="stroke-foreground" strokeDasharray="5 4" opacity="0.6" />
            {showEntryTime && <>
              <line x1={x(entryTime)} y1={top} x2={x(entryTime)} y2={bottom} className="stroke-foreground" strokeDasharray="2 4" opacity="0.5" />
              <circle cx={x(entryTime)} cy={y(entryValue)} r="4" className="fill-foreground" aria-label="Position entry" />
            </>}
          </g>
        )}
        {hovered && <g pointerEvents="none">
          <line x1={x(hovered.time)} y1={top} x2={x(hovered.time)} y2={bottom} className="stroke-muted-foreground" strokeDasharray="2 3" />
          <circle cx={x(hovered.time)} cy={y(hovered.value)} r="3.5" className="fill-primary stroke-background" />
        </g>}
        {Array.from({ length: minTime === maxTime ? 1 : 6 }, (_, index) => {
          const time = minTime + (maxTime - minTime) * index / 5
          return <text key={index} x={x(time)} y={height - 5} className="fill-muted-foreground" fontSize="9" fontFamily="monospace" textAnchor={index === 0 ? 'start' : index === 5 ? 'end' : 'middle'}>{formatTime(time)}</text>
        })}
      </svg>
      <div className="mt-2 flex flex-wrap items-center gap-x-5 gap-y-1 text-[10px] text-muted-foreground">
        <span>2-min median · shaded range · mark prices</span>
        {entryValue !== null && <span>Dashed level: entry from fills</span>}
        <span className="ml-auto">{showPremiumSummary ? '0% = same price · showing available history' : 'Positive = short venue priced higher'}</span>
      </div>
    </>
  )
}

function SpreadMetric({ label, value }: { label: string; value: string }) {
  return <div><p className="mb-0.5 text-[10px] text-muted-foreground">{label}</p><p className="font-mono text-sm font-medium text-foreground">{value}</p></div>
}
