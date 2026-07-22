import { useMemo, useState } from 'react'
import { useHistory, type HistoryPoint } from '@/hooks/useHistory'

interface Props {
  asset: string
  venueA: string
  venueB: string
}

type Timeframe = 'D' | 'W' | 'M'

const rangeMap: Record<Timeframe, string> = { D: '24h', W: '7d', M: '30d' }
const rangeLabel: Record<Timeframe, string> = { D: '24h', W: '7 days', M: '30 days' }
const edgeColor = '#60a5fa'

function formatTime(ts: string, tf: Timeframe) {
  const d = new Date(ts)
  if (tf === 'D') return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function fmtEdge(v: number) {
  return (v * 100).toFixed(2) + '%'
}

function fmtFunding(v: number) {
  return (v * 100).toFixed(4) + '%'
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

export function FundingChart({ asset, venueA, venueB }: Props) {
  const [tf, setTf] = useState<Timeframe>('W')
  const [hoverIdx, setHoverIdx] = useState<number | null>(null)
  const { data, loading, error } = useHistory(asset, venueA, venueB, rangeMap[tf])

  const hovered = hoverIdx !== null ? data[hoverIdx] : null
  const stats = useMemo(() => {
    if (data.length === 0) return null
    return {
      averageEdge: data.reduce((sum, point) => sum + point.edge, 0) / data.length,
      currentEdge: data[data.length - 1].edge,
    }
  }, [data])

  const labelA = venueLabel(venueA)
  const labelB = venueLabel(venueB)
  const colorA = venueColor(venueA)
  const colorB = venueColor(venueB)

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <p className="text-xs font-semibold text-foreground">Funding & Edge History</p>
        <div className="flex gap-0.5 bg-white/[0.04] rounded p-0.5">
          {(['D', 'W', 'M'] as Timeframe[]).map((timeframe) => (
            <button
              key={timeframe}
              onClick={() => { setTf(timeframe); setHoverIdx(null) }}
              className={`px-2 py-0.5 rounded text-[10px] font-medium transition-colors ${
                tf === timeframe ? 'bg-white/[0.08] text-foreground' : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {timeframe}
            </button>
          ))}
        </div>
      </div>

      <div className="h-4 mb-2 text-[11px] text-muted-foreground/60 font-mono truncate">
        {hovered ? (
          <>
            <span className="text-muted-foreground">{formatTime(hovered.t, tf)}</span>
            <span style={{ color: colorA }}> · {labelA} {fmtFunding(hovered.funding_a)}</span>
            <span style={{ color: colorB }}> · {labelB} {fmtFunding(hovered.funding_b)}</span>
            <span style={{ color: edgeColor }}> · Edge {fmtEdge(hovered.edge)}</span>
          </>
        ) : (
          <>Hourly venue funding and annualized gross edge over {rangeLabel[tf]}</>
        )}
      </div>

      <div className="flex items-center gap-4 mb-1 text-[10px] text-muted-foreground">
        <Legend color={colorA} label={`${labelA} hourly`} />
        <Legend color={colorB} label={`${labelB} hourly`} />
        <Legend color={edgeColor} label="Annualized edge" />
      </div>

      {loading ? (
        <ChartLoading />
      ) : error || data.length === 0 ? (
        <div className="flex items-center justify-center h-[158px] text-xs text-muted-foreground">
          {error ? `Error: ${error}` : 'No data yet — snapshots accumulate every minute'}
        </div>
      ) : (
        <FundingLines
          data={data}
          tf={tf}
          hoverIdx={hoverIdx}
          setHoverIdx={setHoverIdx}
          colorA={colorA}
          colorB={colorB}
        />
      )}

      {!loading && stats && (
        <div className="flex items-center gap-4 mt-2 text-[11px] font-mono">
          <div className="flex items-center gap-1.5">
            <div className="size-1.5 rounded-full" style={{ backgroundColor: edgeColor }} />
            <span className="text-muted-foreground">Edge now</span>
            <span style={{ color: edgeColor }}>{fmtEdge(stats.currentEdge)}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <div className="size-1.5 rounded-full bg-blue-400/60" />
            <span className="text-muted-foreground">Avg ({rangeLabel[tf]})</span>
            <span className="text-blue-400">{fmtEdge(stats.averageEdge)}</span>
          </div>
        </div>
      )}

      {!loading && (
        <div className="mt-3 rounded border border-blue-500/10 bg-blue-500/[0.04] px-3 py-1.5 flex items-start gap-2">
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" className="text-blue-400/50 shrink-0 mt-px">
            <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.2" />
            <path d="M8 7v4M8 5.5v.01" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
          <p className="text-[11px] text-blue-300/60 leading-relaxed">
            Funding lines show each venue's hourly rate. Edge is the absolute annualized difference between them, not guaranteed realized yield.
          </p>
        </div>
      )}
    </div>
  )
}

function Legend({ color, label }: { color: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className="w-3 h-px" style={{ backgroundColor: color }} />
      {label}
    </span>
  )
}

function ChartLoading() {
  return (
    <div className="flex flex-col items-center justify-center h-[158px]">
      <div className="relative size-8 animate-[loader-pulse_2s_ease-in-out_infinite]">
        <div className="absolute inset-0 rounded-full border-2 border-slate-500/40" />
        <div className="absolute inset-1.5 rounded-full border-[1.5px] border-slate-400/50" />
        <div className="absolute inset-0 flex items-center justify-center">
          <div className="size-1.5 rounded-full bg-cyan-400 shadow-[0_0_8px_rgba(6,182,212,0.6)]" />
        </div>
      </div>
    </div>
  )
}

function FundingLines({ data, tf, hoverIdx, setHoverIdx, colorA, colorB }: {
  data: HistoryPoint[]
  tf: Timeframe
  hoverIdx: number | null
  setHoverIdx: (index: number | null) => void
  colorA: string
  colorB: string
}) {
  const width = 700
  const height = 140
  const top = 10
  const bottom = 12
  const plotHeight = height - top - bottom
  const maxFunding = Math.max(
    ...data.flatMap((point) => [Math.abs(point.funding_a), Math.abs(point.funding_b)]),
    0.000001,
  )
  const maxEdge = Math.max(...data.map((point) => point.edge), 0.001)
  const x = (index: number) => data.length === 1 ? width / 2 : (index / (data.length - 1)) * width
  const fundingY = (value: number) => top + plotHeight / 2 - (value / maxFunding) * (plotHeight / 2)
  const edgeY = (value: number) => top + plotHeight - (value / maxEdge) * plotHeight
  const path = (value: (point: HistoryPoint) => number, y: (value: number) => number) =>
    data.map((point, index) => `${index === 0 ? 'M' : 'L'} ${x(index).toFixed(2)} ${y(value(point)).toFixed(2)}`).join(' ')
  const fundingAPath = path((point) => point.funding_a, fundingY)
  const fundingBPath = path((point) => point.funding_b, fundingY)
  const edgePath = path((point) => point.edge, edgeY)
  const edgeAreaPath = `${edgePath} L ${width} ${top + plotHeight} L 0 ${top + plotHeight} Z`
  const labelStep = Math.max(1, Math.ceil(data.length / 7))
  const hitWidth = width / Math.max(1, data.length)
  const hovered = hoverIdx !== null ? data[hoverIdx] : null

  return (
    <svg
      key={`${tf}-${data[0]?.t}-${data.length}`}
      viewBox={`0 0 ${width} ${height + 18}`}
      className="w-full"
      onMouseLeave={() => setHoverIdx(null)}
      role="img"
      aria-label="Historical Pacifica funding, Hyperliquid funding, and annualized edge"
    >
      <defs>
        <linearGradient id="edge-area" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={edgeColor} stopOpacity="0.18" />
          <stop offset="75%" stopColor={edgeColor} stopOpacity="0.025" />
          <stop offset="100%" stopColor={edgeColor} stopOpacity="0" />
        </linearGradient>
        <linearGradient id="pacifica-line" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0%" stopColor={colorA} stopOpacity="0.55" />
          <stop offset="100%" stopColor={colorA} />
        </linearGradient>
        <linearGradient id="hyperliquid-line" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0%" stopColor={colorB} stopOpacity="0.55" />
          <stop offset="100%" stopColor={colorB} />
        </linearGradient>
        <filter id="line-glow" x="-20%" y="-30%" width="140%" height="160%">
          <feGaussianBlur stdDeviation="2.2" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>
      <rect x="0" y={top} width={width} height={plotHeight} fill="rgba(255,255,255,0.012)" rx="2" />

      {[-1, -0.5, 0, 0.5, 1].map((tick) => {
        const y = fundingY(tick * maxFunding)
        return (
          <g key={tick}>
            <line x1="0" y1={y} x2={width} y2={y} stroke={tick === 0 ? 'rgba(255,255,255,0.12)' : 'rgba(255,255,255,0.035)'} strokeWidth="1" />
            <text x="3" y={y - 3} fill="#64748b" fontSize="7" fontFamily="monospace" opacity="0.65">
              {fmtFunding(tick * maxFunding)}
            </text>
          </g>
        )
      })}

      {[0, 0.5, 1].map((tick) => {
        const y = edgeY(tick * maxEdge)
        return (
          <text key={tick} x={width - 3} y={y - 3} fill={edgeColor} fontSize="7" fontFamily="monospace" textAnchor="end" opacity="0.65">
            {fmtEdge(tick * maxEdge)}
          </text>
        )
      })}

      <path d={edgeAreaPath} fill="url(#edge-area)" opacity="0">
        <animate attributeName="opacity" from="0" to="1" dur="0.7s" begin="0.55s" fill="freeze" />
      </path>

      <AnimatedLine d={fundingAPath} stroke="url(#pacifica-line)" glowColor={colorA} delay={0.05} />
      <AnimatedLine d={fundingBPath} stroke="url(#hyperliquid-line)" glowColor={colorB} delay={0.18} />
      <AnimatedLine d={edgePath} stroke={edgeColor} glowColor={edgeColor} delay={0.32} emphasis />

      {data.map((_, index) => (
        <rect
          key={index}
          x={Math.max(0, x(index) - hitWidth / 2)}
          y={top}
          width={hitWidth}
          height={plotHeight}
          fill="transparent"
          onMouseEnter={() => setHoverIdx(index)}
        />
      ))}

      {hoverIdx !== null && hovered && (
        <g>
          <line x1={x(hoverIdx)} y1={top} x2={x(hoverIdx)} y2={top + plotHeight} stroke="rgba(255,255,255,0.18)" strokeWidth="1" strokeDasharray="2,2" />
          <circle cx={x(hoverIdx)} cy={fundingY(hovered.funding_a)} r="3" fill={colorA} stroke="#080b12" strokeWidth="1" />
          <circle cx={x(hoverIdx)} cy={fundingY(hovered.funding_b)} r="3" fill={colorB} stroke="#080b12" strokeWidth="1" />
          <circle cx={x(hoverIdx)} cy={edgeY(hovered.edge)} r="3" fill={edgeColor} stroke="#080b12" strokeWidth="1" />
        </g>
      )}

      {data.map((point, index) => {
        if (index % labelStep !== 0) return null
        return (
          <text key={point.t} x={Math.max(18, Math.min(width - 18, x(index)))} y={height + 10} textAnchor="middle" fill="#64748b" fontSize="7" fontFamily="monospace">
            {formatTime(point.t, tf)}
          </text>
        )
      })}
    </svg>
  )
}

function AnimatedLine({ d, stroke, glowColor, delay, emphasis = false }: {
  d: string
  stroke: string
  glowColor: string
  delay: number
  emphasis?: boolean
}) {
  const duration = emphasis ? 0.95 : 0.8
  return (
    <g>
      <path
        d={d}
        fill="none"
        stroke={glowColor}
        strokeWidth={emphasis ? 5 : 4}
        strokeLinejoin="round"
        strokeLinecap="round"
        opacity="0"
        filter="url(#line-glow)"
        vectorEffect="non-scaling-stroke"
      >
        <animate attributeName="opacity" from="0" to={emphasis ? '0.13' : '0.09'} dur="0.4s" begin={`${delay + 0.25}s`} fill="freeze" />
      </path>
      <path
        d={d}
        fill="none"
        stroke={stroke}
        strokeWidth={emphasis ? 2.2 : 1.7}
        strokeLinejoin="round"
        strokeLinecap="round"
        pathLength="1"
        strokeDasharray="1"
        strokeDashoffset="1"
        vectorEffect="non-scaling-stroke"
      >
        <animate attributeName="stroke-dashoffset" from="1" to="0" dur={`${duration}s`} begin={`${delay}s`} fill="freeze" calcMode="spline" keyTimes="0;1" keySplines="0.22 1 0.36 1" />
      </path>
    </g>
  )
}
