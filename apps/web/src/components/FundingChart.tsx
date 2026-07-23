import { useId, useMemo, useState, type PointerEvent } from 'react'
import { useHistory, type HistoryPoint } from '@/hooks/useHistory'
import {
  annualizeFunding,
  calculateFundingStats,
  calculateReturnProjection,
  directionalCarry,
  paddedChartDomain,
  type FundingDirection,
  type ReturnProjection,
} from '@/lib/funding-chart'

interface Props {
  asset: string
  venueA: string
  venueB: string
  direction: FundingDirection
  recommendedNotional: number
  feeEstimate: number
  slippageEstimate: number
}

type Timeframe = 'D' | 'W' | 'M'
type ChartView = 'funding' | 'return'

const rangeMap: Record<Timeframe, string> = { D: '24h', W: '7d', M: '30d' }
const rangeLabel: Record<Timeframe, string> = { D: '24h', W: '7 days', M: '30 days' }
const horizonHours: Record<Timeframe, number> = { D: 24, W: 24 * 7, M: 24 * 30 }

function formatTime(ts: string | number, tf: Timeframe) {
  const date = new Date(ts)
  if (tf === 'D') return date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function formatRate(value: number, signed = false) {
  const percent = value * 100
  const sign = signed && percent > 0 ? '+' : ''
  return `${sign}${percent.toFixed(Math.abs(percent) >= 10 ? 1 : 2)}%`
}

function formatUsd(value: number) {
  const sign = value > 0 ? '+' : value < 0 ? '-' : ''
  return `${sign}$${Math.abs(value).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

function formatUsdAxis(value: number, domainSpan: number) {
  const digits = domainSpan < 0.1 ? 3 : 2
  const sign = value > 0 ? '+' : value < 0 ? '-' : ''
  return `${sign}$${Math.abs(value).toFixed(digits)}`
}

function formatNotional(value: number) {
  if (value >= 1_000_000) return `$${(value / 1_000_000).toFixed(value % 1_000_000 === 0 ? 0 : 1)}m`
  if (value >= 1_000) return `$${(value / 1_000).toFixed(value % 1_000 === 0 ? 0 : 1)}k`
  return `$${Math.round(value)}`
}

function venueLabel(venue: string) {
  if (venue.toLowerCase() === 'hyperliquid') return 'Hyperliquid'
  return venue.charAt(0).toUpperCase() + venue.slice(1)
}

function venueColor(venue: string) {
  if (venue.toLowerCase() === 'pacifica') return '#22d3ee'
  if (venue.toLowerCase() === 'hyperliquid') return '#a78bfa'
  return '#f59e0b'
}

export function FundingChart({
  asset,
  venueA,
  venueB,
  direction,
  recommendedNotional,
  feeEstimate,
  slippageEstimate,
}: Props) {
  const [view, setView] = useState<ChartView>('funding')
  const [tf, setTf] = useState<Timeframe>('W')
  const [hoverIdx, setHoverIdx] = useState<number | null>(null)
  const defaultNotional = Math.max(1, recommendedNotional || 10_000)
  const [notionalOverride, setNotionalOverride] = useState<number | null>(null)
  const notional = notionalOverride ?? defaultNotional
  const { data, loading, error } = useHistory(asset, venueA, venueB, rangeMap[tf])

  const stats = useMemo(() => calculateFundingStats(data, direction), [data, direction])
  const projection = useMemo(
    () => calculateReturnProjection(
      data,
      direction,
      notional,
      feeEstimate + slippageEstimate,
      horizonHours[tf],
    ),
    [data, direction, feeEstimate, notional, slippageEstimate, tf],
  )
  const notionalOptions = useMemo(
    () => Array.from(new Set([defaultNotional, 1_000, 5_000, 10_000])),
    [defaultNotional],
  )

  const labelA = venueLabel(venueA)
  const labelB = venueLabel(venueB)
  const colorA = venueColor(venueA)
  const colorB = venueColor(venueB)
  const isLongA = direction === 'long_a_short_b'
  const directionLabel = `Long ${isLongA ? labelA : labelB} / Short ${isLongA ? labelB : labelA}`
  const hovered = hoverIdx !== null ? data[hoverIdx] : null

  const changeView = (nextView: ChartView) => {
    setView(nextView)
    setHoverIdx(null)
  }

  return (
    <section className="rounded-lg border border-white/[0.07] bg-[#090d14]/70 overflow-hidden">
      <div className="flex items-end justify-between gap-4 border-b border-white/[0.07] px-4 pt-3">
        <div className="flex h-9 items-end gap-5" role="tablist" aria-label="Opportunity chart">
          <ChartTab active={view === 'funding'} onClick={() => changeView('funding')}>
            Funding Rates
          </ChartTab>
          <ChartTab active={view === 'return'} onClick={() => changeView('return')}>
            Potential Return
          </ChartTab>
        </div>
        <div className="mb-2 flex gap-0.5 rounded bg-white/[0.04] p-0.5">
          {(['D', 'W', 'M'] as Timeframe[]).map((timeframe) => (
            <button
              key={timeframe}
              onClick={() => { setTf(timeframe); setHoverIdx(null) }}
              className={`rounded px-2 py-0.5 text-[10px] font-medium transition-colors ${
                tf === timeframe ? 'bg-white/[0.08] text-foreground' : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {timeframe}
            </button>
          ))}
        </div>
      </div>

      <div className="p-4">
        {loading ? (
          <ChartLoading />
        ) : error || data.length === 0 ? (
          <div className="flex h-[272px] items-center justify-center text-xs text-muted-foreground">
            {error ? `Error: ${error}` : 'No history yet. Funding snapshots will appear here as they accumulate.'}
          </div>
        ) : view === 'funding' && stats ? (
          <>
            <div className="grid grid-cols-3 gap-3 mb-3 max-w-xl">
              <Metric label="Current carry" value={formatRate(stats.currentCarry, true)} tone={stats.currentCarry} />
              <Metric label={`${rangeLabel[tf]} average`} value={formatRate(stats.averageCarry, true)} tone={stats.averageCarry} />
              <Metric label="Positive periods" value={`${Math.round(stats.positiveShare * 100)}%`} />
            </div>

            <div className="h-5 mb-1 text-[11px] font-mono text-muted-foreground truncate">
              {hovered ? (
                <>
                  <span>{formatTime(hovered.t, tf)}</span>
                  <span style={{ color: colorA }}> · {labelA} {formatRate(annualizeFunding(hovered.funding_a), true)}</span>
                  <span style={{ color: colorB }}> · {labelB} {formatRate(annualizeFunding(hovered.funding_b), true)}</span>
                  <span className={directionalCarry(hovered, direction) >= 0 ? 'text-emerald-400' : 'text-rose-400'}>
                    {' '}· Carry {formatRate(annualizeFunding(directionalCarry(hovered, direction)), true)}
                  </span>
                </>
              ) : directionLabel}
            </div>

            <FundingRateChart
              key={`funding-${tf}-${data[0]?.t}-${data.length}`}
              data={data}
              tf={tf}
              hoverIdx={hoverIdx}
              setHoverIdx={setHoverIdx}
              colorA={colorA}
              colorB={colorB}
              direction={direction}
            />

            <div className="mt-2 flex flex-wrap items-center gap-x-5 gap-y-1 text-[10px] text-muted-foreground">
              <Legend color={colorA} label={labelA} />
              <Legend color={colorB} label={labelB} />
              <span className="flex items-center gap-1.5">
                <span className="flex gap-px"><span className="h-2 w-1 bg-emerald-400/70" /><span className="h-2 w-1 bg-rose-400/70" /></span>
                Bars show net carry
              </span>
              <span className="ml-auto text-muted-foreground/60">Shading shows the funding gap · {directionLabel}</span>
            </div>
          </>
        ) : projection ? (
          <>
            <div className="flex flex-wrap items-end justify-between gap-3 mb-3">
              <div className="grid grid-cols-3 gap-3 flex-1 max-w-xl">
                <Metric label="Potential return" value={formatUsd(projection.potentialReturn)} tone={projection.potentialReturn} />
                <Metric label="Break-even time" value={formatBreakEven(projection.breakEvenHours, horizonHours[tf])} />
                <Metric label="Estimated costs" value={`$${projection.costs.toFixed(2)}`} />
              </div>
              <div>
                <p className="mb-1.5 text-right text-[10px] text-muted-foreground">Position size</p>
                <div className="flex gap-1">
                  {notionalOptions.map((value) => (
                    <button
                      key={value}
                      onClick={() => setNotionalOverride(value === defaultNotional ? null : value)}
                      className={`rounded border px-2 py-1 text-[10px] font-mono transition-colors ${
                        notional === value
                          ? 'border-blue-400/30 bg-blue-400/10 text-blue-300'
                          : 'border-white/[0.07] text-muted-foreground hover:border-white/15 hover:text-foreground'
                      }`}
                    >
                      {formatNotional(value)}{value === defaultNotional ? ' rec.' : ''}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <PotentialReturnChart
              key={`return-${tf}-${notional}-${data[0]?.t}-${data.length}`}
              projection={projection}
              notional={notional}
              tf={tf}
            />

            <div className="mt-2 flex flex-wrap items-center gap-x-5 gap-y-1 text-[10px] text-muted-foreground">
              <span className="flex items-center gap-1.5"><span className="w-3 h-px bg-blue-400" /> Base estimate</span>
              <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm bg-blue-400/15" /> Range from history</span>
              <span className="ml-auto text-muted-foreground/60">Based on historical funding rates. Actual results may vary.</span>
            </div>
          </>
        ) : null}
      </div>
    </section>
  )
}

function ChartTab({ active, onClick, children }: { active: boolean; onClick: () => void; children: string }) {
  return (
    <button
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={`relative h-9 pb-2 text-xs font-medium transition-colors ${active ? 'text-foreground' : 'text-muted-foreground hover:text-foreground'}`}
    >
      {children}
      {active && <span className="absolute inset-x-0 -bottom-px h-px bg-blue-400" />}
    </button>
  )
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: number }) {
  const toneClass = tone === undefined ? 'text-foreground' : tone > 0 ? 'text-emerald-400' : tone < 0 ? 'text-rose-400' : 'text-foreground'
  return (
    <div>
      <p className="text-[10px] text-muted-foreground mb-0.5">{label}</p>
      <p className={`text-sm font-mono font-medium ${toneClass}`}>{value}</p>
    </div>
  )
}

function Legend({ color, label }: { color: string; label: string }) {
  return <span className="flex items-center gap-1.5"><span className="w-3 h-px" style={{ backgroundColor: color }} />{label}</span>
}

function ChartLoading() {
  return (
    <div className="flex h-[272px] items-center justify-center">
      <div className="size-5 animate-spin rounded-full border border-slate-500/40 border-t-cyan-400" />
    </div>
  )
}

interface PlotPoint {
  x: number
  y: number
}

function FundingRateChart({ data, tf, hoverIdx, setHoverIdx, colorA, colorB, direction }: {
  data: HistoryPoint[]
  tf: Timeframe
  hoverIdx: number | null
  setHoverIdx: (index: number | null) => void
  colorA: string
  colorB: string
  direction: FundingDirection
}) {
  const revealId = `funding-reveal-${useId().replace(/:/g, '')}`
  const width = 1100
  const height = 210
  const left = 58
  const right = 32
  const plotTop = 10
  const plotBottom = 126
  const carryTop = 147
  const carryBottom = 177
  const plotWidth = width - left - right
  const timestamps = data.map((point) => new Date(point.t).getTime())
  const minTime = Math.min(...timestamps)
  const maxTime = Math.max(...timestamps)
  const x = (timestamp: number) => left + ((timestamp - minTime) / Math.max(1, maxTime - minTime)) * plotWidth
  const values = data.flatMap((point) => [annualizeFunding(point.funding_a), annualizeFunding(point.funding_b)])
  const minValue = Math.min(0, ...values)
  const maxValue = Math.max(0, ...values)
  const padding = Math.max((maxValue - minValue) * 0.12, 0.005)
  const domainMin = minValue - padding
  const domainMax = maxValue + padding
  const y = (value: number) => plotBottom - ((value - domainMin) / (domainMax - domainMin)) * (plotBottom - plotTop)
  const segments = [data]
  const carryValues = data.map((point) => annualizeFunding(directionalCarry(point, direction)))
  const maxCarry = Math.max(...carryValues.map(Math.abs), 0.001)
  const carryMid = (carryTop + carryBottom) / 2
  const carryY = (value: number) => carryMid - (value / maxCarry) * ((carryBottom - carryTop) / 2)
  const barWidth = Math.max(1, Math.min(5, plotWidth / data.length * 0.7))
  const hoveredX = hoverIdx === null ? null : x(timestamps[hoverIdx])
  const ticks = Array.from({ length: 5 }, (_, index) => domainMin + (domainMax - domainMin) * (index / 4))

  const handlePointer = (event: PointerEvent<SVGSVGElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    const svgX = ((event.clientX - rect.left) / rect.width) * width
    setHoverIdx(nearestTimestampIndex(timestamps, minTime + ((svgX - left) / plotWidth) * (maxTime - minTime)))
  }

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className="w-full touch-none"
      onPointerMove={handlePointer}
      onPointerLeave={() => setHoverIdx(null)}
      role="img"
      aria-label="Annualized venue funding rates and directional carry history"
    >
      <defs>
        <RevealClip id={revealId} x={left} y={plotTop} width={plotWidth} height={carryBottom - plotTop} />
      </defs>

      {ticks.map((tick) => (
        <g key={tick}>
          <line x1={left} y1={y(tick)} x2={width - right} y2={y(tick)} stroke="rgba(255,255,255,0.05)" />
          <text x={left - 8} y={y(tick) + 3} fill="#64748b" fontSize="8" fontFamily="monospace" textAnchor="end">
            {formatRate(tick)}
          </text>
        </g>
      ))}
      <line x1={left} y1={y(0)} x2={width - right} y2={y(0)} stroke="rgba(255,255,255,0.16)" />

      <text x={left} y={carryTop - 7} fill="#64748b" fontSize="8" fontFamily="monospace">NET CARRY</text>
      <line x1={left} y1={carryMid} x2={width - right} y2={carryMid} stroke="rgba(255,255,255,0.08)" />
      <g clipPath={`url(#${revealId})`}>
        {segments.map((segment, index) => {
          const a = segment.map((point) => ({ x: x(new Date(point.t).getTime()), y: y(annualizeFunding(point.funding_a)) }))
          const b = segment.map((point) => ({ x: x(new Date(point.t).getTime()), y: y(annualizeFunding(point.funding_b)) }))
          const area = [...a, ...b.toReversed()].map((point, pointIndex) => `${pointIndex === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' ') + ' Z'
          return (
            <g key={`${segment[0].t}-${index}`}>
              <path d={area} fill="#60a5fa" opacity="0.055" />
              <path d={monotonePath(a)} fill="none" stroke={colorA} strokeWidth="1.6" vectorEffect="non-scaling-stroke" />
              <path d={monotonePath(b)} fill="none" stroke={colorB} strokeWidth="1.6" vectorEffect="non-scaling-stroke" />
            </g>
          )
        })}
        {data.map((point, index) => {
          const value = carryValues[index]
          const barY = carryY(value)
          return (
            <rect
              key={point.t}
              x={x(timestamps[index]) - barWidth / 2}
              y={Math.min(carryMid, barY)}
              width={barWidth}
              height={Math.max(1, Math.abs(carryMid - barY))}
              rx="0.5"
              fill={value >= 0 ? '#34d399' : '#fb7185'}
              opacity={hoverIdx === index ? 1 : 0.58}
            />
          )
        })}
      </g>

      {Array.from({ length: 6 }, (_, index) => {
        const timestamp = minTime + (maxTime - minTime) * (index / 5)
        return (
          <text key={timestamp} x={x(timestamp)} y={height - 5} fill="#64748b" fontSize="8" fontFamily="monospace" textAnchor={index === 0 ? 'start' : index === 5 ? 'end' : 'middle'}>
            {formatTime(timestamp, tf)}
          </text>
        )
      })}

      {hoveredX !== null && hoverIdx !== null && (
        <g pointerEvents="none">
          <line x1={hoveredX} y1={plotTop} x2={hoveredX} y2={carryBottom} stroke="rgba(255,255,255,0.2)" strokeDasharray="2 3" />
          <circle cx={hoveredX} cy={y(annualizeFunding(data[hoverIdx].funding_a))} r="2.5" fill={colorA} stroke="#090d14" />
          <circle cx={hoveredX} cy={y(annualizeFunding(data[hoverIdx].funding_b))} r="2.5" fill={colorB} stroke="#090d14" />
        </g>
      )}
    </svg>
  )
}

function PotentialReturnChart({ projection, notional, tf }: { projection: ReturnProjection; notional: number; tf: Timeframe }) {
  const clipPositive = `return-positive-${useId().replace(/:/g, '')}`
  const clipNegative = `return-negative-${useId().replace(/:/g, '')}`
  const revealId = `return-reveal-${useId().replace(/:/g, '')}`
  const width = 1100
  const height = 210
  const left = 58
  const right = 24
  const top = 10
  const bottom = 178
  const plotWidth = width - left - right
  const horizon = horizonHours[tf]
  const steps = 32
  const values = Array.from({ length: steps + 1 }, (_, index) => {
    const hours = horizon * (index / steps)
    return {
      hours,
      base: notional * projection.baseCarryPerHour * hours - projection.costs,
      lower: notional * projection.lowerCarryPerHour * hours - projection.costs,
      upper: notional * projection.upperCarryPerHour * hours - projection.costs,
    }
  })
  const allValues = values.flatMap((point) => [point.base, point.lower, point.upper, 0])
  const { min: domainMin, max: domainMax } = paddedChartDomain(allValues, 0.02)
  const domainSpan = domainMax - domainMin
  const x = (hours: number) => left + (hours / horizon) * plotWidth
  const y = (value: number) => bottom - ((value - domainMin) / (domainMax - domainMin)) * (bottom - top)
  const zeroY = y(0)
  const basePoints = values.map((point) => ({ x: x(point.hours), y: y(point.base) }))
  const upperPoints = values.map((point) => ({ x: x(point.hours), y: y(point.upper) }))
  const lowerPoints = values.toReversed().map((point) => ({ x: x(point.hours), y: y(point.lower) }))
  const bandPath = [...upperPoints, ...lowerPoints].map((point, index) => `${index === 0 ? 'M' : 'L'} ${point.x} ${point.y}`).join(' ') + ' Z'
  const areaPath = `${monotonePath(basePoints)} L ${width - right} ${zeroY} L ${left} ${zeroY} Z`
  const breakEvenX = projection.breakEvenHours !== null
    && projection.breakEvenHours > horizon * 0.01
    && projection.breakEvenHours <= horizon
    ? x(projection.breakEvenHours)
    : null
  const ticks = Array.from({ length: 5 }, (_, index) => domainMin + (domainMax - domainMin) * (index / 4))
  const endpoint = basePoints[basePoints.length - 1]
  const endpointLabelY = endpoint.y < top + 14 ? endpoint.y + 16 : endpoint.y - 8

  return (
    <svg viewBox={`0 0 ${width} ${height}`} className="w-full" role="img" aria-label="Potential return projection after estimated costs">
      <defs>
        <clipPath id={clipPositive}><rect x={left} y={top} width={plotWidth} height={Math.max(0, zeroY - top)} /></clipPath>
        <clipPath id={clipNegative}><rect x={left} y={zeroY} width={plotWidth} height={Math.max(0, bottom - zeroY)} /></clipPath>
        <RevealClip id={revealId} x={left} y={top} width={plotWidth} height={bottom - top} />
      </defs>

      {ticks.map((tick) => (
        <g key={tick}>
          <line x1={left} y1={y(tick)} x2={width - right} y2={y(tick)} stroke="rgba(255,255,255,0.05)" />
          <text x={left - 8} y={y(tick) + 3} fill="#64748b" fontSize="8" fontFamily="monospace" textAnchor="end">
            {formatUsdAxis(tick, domainSpan)}
          </text>
        </g>
      ))}
      <line x1={left} y1={zeroY} x2={width - right} y2={zeroY} stroke="rgba(255,255,255,0.18)" />

      <g clipPath={`url(#${revealId})`}>
        <path d={bandPath} fill="#60a5fa" opacity="0.09" />
        <path d={areaPath} fill="#34d399" opacity="0.16" clipPath={`url(#${clipPositive})`} />
        <path d={areaPath} fill="#fb7185" opacity="0.14" clipPath={`url(#${clipNegative})`} />
        <path d={monotonePath(basePoints)} fill="none" stroke="#60a5fa" strokeWidth="1.8" vectorEffect="non-scaling-stroke" />
        <path d={monotonePath(upperPoints)} fill="none" stroke="#60a5fa" strokeWidth="0.8" strokeDasharray="3 4" opacity="0.4" vectorEffect="non-scaling-stroke" />
        <path d={monotonePath(values.map((point) => ({ x: x(point.hours), y: y(point.lower) })))} fill="none" stroke="#60a5fa" strokeWidth="0.8" strokeDasharray="3 4" opacity="0.4" vectorEffect="non-scaling-stroke" />

        {projection.costs >= 0.005 && (
          <>
            <circle cx={left} cy={y(-projection.costs)} r="2.5" fill="#fb7185" />
            <text x={left + 6} y={y(-projection.costs) - 6} fill="#94a3b8" fontSize="8" fontFamily="monospace">ENTRY COST</text>
          </>
        )}

        {breakEvenX !== null && (
          <g>
            <line x1={breakEvenX} y1={top} x2={breakEvenX} y2={bottom} stroke="rgba(52,211,153,0.35)" strokeDasharray="2 3" />
            <circle cx={breakEvenX} cy={zeroY} r="3" fill="#34d399" stroke="#090d14" />
            <text x={breakEvenX + 6} y={zeroY - 7} fill="#6ee7b7" fontSize="8" fontFamily="monospace">BREAK-EVEN</text>
          </g>
        )}
      </g>

      <g className="chart-endpoint" pointerEvents="none">
        <circle cx={endpoint.x} cy={endpoint.y} r="3" fill={projection.potentialReturn >= 0 ? '#34d399' : '#fb7185'} stroke="#090d14" />
        <text x={endpoint.x - 7} y={endpointLabelY} fill={projection.potentialReturn >= 0 ? '#6ee7b7' : '#fda4af'} fontSize="10" fontFamily="monospace" fontWeight="600" textAnchor="end">
          {formatUsd(projection.potentialReturn)}
        </text>
      </g>

      {Array.from({ length: 6 }, (_, index) => {
        const hours = horizon * (index / 5)
        return (
          <text key={hours} x={x(hours)} y={height - 5} fill="#64748b" fontSize="8" fontFamily="monospace" textAnchor={index === 0 ? 'start' : index === 5 ? 'end' : 'middle'}>
            {formatHorizon(hours)}
          </text>
        )
      })}
    </svg>
  )
}

function RevealClip({ id, x, y, width, height }: { id: string; x: number; y: number; width: number; height: number }) {
  return (
    <clipPath id={id}>
      <rect className="chart-reveal" x={x} y={y} width={width} height={height} />
    </clipPath>
  )
}

function formatBreakEven(hours: number | null, horizon: number) {
  if (hours === null) return 'Not projected'
  if (hours <= 0.05) return 'Immediate'
  if (hours > horizon) return `Beyond ${formatHorizon(horizon)}`
  if (hours < 1) return `${Math.round(hours * 60)}m`
  if (hours < 48) return `${hours.toFixed(1)}h`
  return `${(hours / 24).toFixed(1)}d`
}

function formatHorizon(hours: number) {
  if (hours === 0) return 'Entry'
  if (hours < 48) return `${Math.round(hours)}h`
  return `${Math.round(hours / 24)}d`
}

function nearestTimestampIndex(timestamps: number[], target: number) {
  let nearest = 0
  let distance = Number.POSITIVE_INFINITY
  timestamps.forEach((timestamp, index) => {
    const nextDistance = Math.abs(timestamp - target)
    if (nextDistance < distance) {
      nearest = index
      distance = nextDistance
    }
  })
  return nearest
}

// Fritsch-Carlson tangents keep the visual smoothing inside each data segment,
// avoiding the invented peaks produced by ordinary chart splines.
function monotonePath(points: PlotPoint[]) {
  if (points.length === 0) return ''
  if (points.length === 1) return `M ${points[0].x} ${points[0].y}`

  const slopes = points.slice(1).map((point, index) => (point.y - points[index].y) / Math.max(0.0001, point.x - points[index].x))
  const tangents = points.map((_, index) => {
    if (index === 0) return slopes[0]
    if (index === points.length - 1) return slopes[slopes.length - 1]
    if (slopes[index - 1] * slopes[index] <= 0) return 0
    return (slopes[index - 1] + slopes[index]) / 2
  })

  slopes.forEach((slope, index) => {
    if (slope === 0) {
      tangents[index] = 0
      tangents[index + 1] = 0
      return
    }
    const alpha = tangents[index] / slope
    const beta = tangents[index + 1] / slope
    const magnitude = alpha * alpha + beta * beta
    if (magnitude > 9) {
      const scale = 3 / Math.sqrt(magnitude)
      tangents[index] = scale * alpha * slope
      tangents[index + 1] = scale * beta * slope
    }
  })

  return points.slice(1).reduce((path, point, index) => {
    const previous = points[index]
    const dx = point.x - previous.x
    return `${path} C ${previous.x + dx / 3} ${previous.y + tangents[index] * dx / 3}, ${point.x - dx / 3} ${point.y - tangents[index + 1] * dx / 3}, ${point.x} ${point.y}`
  }, `M ${points[0].x} ${points[0].y}`)
}
