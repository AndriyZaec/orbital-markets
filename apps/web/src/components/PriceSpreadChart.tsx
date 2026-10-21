import { useState, type PointerEvent } from 'react'
import type { HistoryPoint } from '@/hooks/useHistory'
import { paddedChartDomain, type FundingDirection } from '@/lib/funding-chart'

export type SpreadRange = '1h' | '24h' | '7d'
export interface SpreadEntry {
  value: number
  time?: string
}

function formatSpread(value: number) {
  return `${value > 0 ? '+' : ''}${(value * 100).toFixed(4)}%`
}

export function PriceSpreadChart({ data, direction, range, directionLabel, entry }: {
  data: HistoryPoint[]
  direction: FundingDirection
  range: SpreadRange
  directionLabel: string
  entry?: SpreadEntry
}) {
  const [hoverTime, setHoverTime] = useState<number | null>(null)
  const sign = direction === 'long_a_short_b' ? 1 : -1
  const samples = data.map((point) => ({
    time: Date.parse(point.t),
    value: typeof point.price_spread === 'number' && Number.isFinite(point.price_spread) ? point.price_spread * sign : null,
  })).filter((point) => Number.isFinite(point.time))
  const valid = samples.filter((point): point is { time: number; value: number } => point.value !== null)
  if (valid.length === 0) {
    return <div className="flex h-[272px] items-center justify-center text-xs text-muted-foreground">No price spread history for this period.</div>
  }

  const entryValue = entry && Number.isFinite(entry.value) ? entry.value : null
  const latest = valid[valid.length - 1].value
  const hovered = hoverTime === null ? null : valid.reduce((nearest, point) => Math.abs(point.time - hoverTime) < Math.abs(nearest.time - hoverTime) ? point : nearest)
  const width = 1100
  const height = 210
  const left = 78
  const right = 24
  const top = 12
  const bottom = 178
  const minTime = samples[0].time
  const maxTime = samples[samples.length - 1].time
  const timeSpan = Math.max(1, maxTime - minTime)
  const x = (time: number) => left + (time - minTime) / timeSpan * (width - left - right)
  const values = valid.map((point) => point.value)
  const { min, max } = paddedChartDomain([...values, 0, ...(entryValue === null ? [] : [entryValue])], 0.0001)
  const y = (value: number) => bottom - (value - min) / (max - min) * (bottom - top)
  const maxGap = { '1h': 3 * 60_000, '24h': 15 * 60_000, '7d': 3 * 3_600_000 }[range]
  const path = samples.map((point, index) => {
    if (point.value === null) return ''
    const previous = samples[index - 1]
    const command = !previous || previous.value === null || point.time - previous.time > maxGap ? 'M' : 'L'
    return `${command} ${x(point.time)} ${y(point.value)}`
  }).join(' ')
  const entryTime = entry?.time ? Date.parse(entry.time) : NaN
  const showEntryTime = entryValue !== null && entryTime >= minTime && entryTime <= maxTime
  const formatTime = (time: number) => range === '7d'
    ? new Date(time).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
    : new Date(time).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  const handlePointer = (event: PointerEvent<SVGSVGElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    if (rect.width <= 0) return
    const svgX = (event.clientX - rect.left) / rect.width * width
    setHoverTime(minTime + (svgX - left) / (width - left - right) * timeSpan)
  }

  return (
    <>
      <div className="mb-3 grid max-w-xl grid-cols-3 gap-3">
        <SpreadMetric label="Latest spread" value={formatSpread(latest)} />
        <SpreadMetric label={entryValue === null ? 'Period low' : 'Entry spread'} value={formatSpread(entryValue ?? Math.min(...values))} />
        <SpreadMetric label={entryValue === null ? 'Period high' : 'Change since entry'} value={entryValue === null ? formatSpread(Math.max(...values)) : `${latest - entryValue > 0 ? '+' : ''}${((latest - entryValue) * 100).toFixed(4)} pp`} />
      </div>
      <div className="mb-1 h-5 truncate font-mono text-[11px] text-muted-foreground">
        {hovered ? `${new Date(hovered.time).toLocaleString()} · Spread ${formatSpread(hovered.value)}` : directionLabel}
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
        <path d={path} fill="none" className="stroke-primary" strokeWidth="1.8" vectorEffect="non-scaling-stroke" />
        {valid.map((point) => <circle key={point.time} cx={x(point.time)} cy={y(point.value)} r="1.8" className="fill-primary" />)}
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
        <span>Mark-price spread · (Short − Long) / midpoint</span>
        {entryValue !== null && <span>Dashed level: entry from fills</span>}
        <span className="ml-auto">Positive = short venue priced higher</span>
      </div>
    </>
  )
}

function SpreadMetric({ label, value }: { label: string; value: string }) {
  return <div><p className="mb-0.5 text-[10px] text-muted-foreground">{label}</p><p className="font-mono text-sm font-medium text-foreground">{value}</p></div>
}
