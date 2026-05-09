import { useState, useMemo, useEffect, useRef } from 'react'
import { useHistory } from '@/hooks/useHistory'

interface Props {
  asset: string
  venueA: string
  venueB: string
}

type Timeframe = 'D' | 'W' | 'M'

const rangeMap: Record<Timeframe, string> = { D: '24h', W: '7d', M: '30d' }
const rangeLabel: Record<Timeframe, string> = { D: '24h', W: '7 days', M: '30 days' }

function formatTime(ts: string, tf: Timeframe) {
  const d = new Date(ts)
  if (tf === 'D') return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function fmtPct(v: number) {
  return (v * 100).toFixed(2) + '%'
}

export function FundingChart({ asset, venueA, venueB }: Props) {
  const [tf, setTf] = useState<Timeframe>('W')
  const [hoverIdx, setHoverIdx] = useState<number | null>(null)
  const { data, loading, error } = useHistory(asset, venueA, venueB, rangeMap[tf])

  const hovered = hoverIdx !== null ? data[hoverIdx] : null

  const stats = useMemo(() => {
    if (data.length === 0) return null
    const avg = data.reduce((s, d) => s + d.edge, 0) / data.length
    const current = data[data.length - 1].edge
    return { avg, current }
  }, [data])

  return (
    <div>
      {/* Header */}
      <div className="flex items-center justify-between mb-1">
        <p className="text-xs font-semibold text-foreground">Edge Persistence</p>
        <div className="flex gap-0.5 bg-white/[0.04] rounded p-0.5">
          {(['D', 'W', 'M'] as Timeframe[]).map((t) => (
            <button
              key={t}
              onClick={() => setTf(t)}
              className={`px-2 py-0.5 rounded text-[10px] font-medium transition-colors ${
                tf === t ? 'bg-white/[0.08] text-foreground' : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {t}
            </button>
          ))}
        </div>
      </div>
      <p className="text-[11px] text-muted-foreground/50 mb-2 h-4">
        {hovered ? (
          <span>
            {formatTime(hovered.t, tf)}
            {'  '}
            <span className="text-green-400 font-mono">{fmtPct(hovered.edge)}</span>
            <span className="text-muted-foreground/40"> annualized edge</span>
          </span>
        ) : (
          <>How consistently this spread has generated edge over the last {rangeLabel[tf]}</>
        )}
      </p>

      {/* Chart */}
      {loading ? (
        <div className="flex items-center justify-center h-[140px] text-xs text-muted-foreground">Loading...</div>
      ) : error || data.length === 0 ? (
        <div className="flex items-center justify-center h-[140px] text-xs text-muted-foreground">
          {error ? `Error: ${error}` : 'No data yet — snapshots accumulate every minute'}
        </div>
      ) : (
        <EdgeChart data={data} tf={tf} hoverIdx={hoverIdx} setHoverIdx={setHoverIdx} avgEdge={stats?.avg ?? 0} />
      )}

      {/* Stats bar */}
      {stats && (
        <div className="flex items-center gap-4 mt-2 text-[11px] font-mono">
          <div className="flex items-center gap-1.5">
            <div className="size-1.5 rounded-full bg-green-400" />
            <span className="text-muted-foreground">Now</span>
            <span className="text-green-400">{fmtPct(stats.current)}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <div className="size-1.5 rounded-full bg-blue-400/60" />
            <span className="text-muted-foreground">Avg ({rangeLabel[tf]})</span>
            <span className="text-blue-400">{fmtPct(stats.avg)}</span>
          </div>
        </div>
      )}

      {/* Caption */}
      <div className="mt-3 rounded border border-blue-500/10 bg-blue-500/[0.04] px-3 py-1.5 flex items-start gap-2">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" className="text-blue-400/50 shrink-0 mt-px">
          <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.2"/>
          <path d="M8 7v4M8 5.5v.01" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>
        </svg>
        <p className="text-[11px] text-blue-300/60 leading-relaxed">
          A persistent edge means the funding spread isn't a one-time spike — the opportunity has held over time, making it more reliable to trade.
        </p>
      </div>
    </div>
  )
}

function EdgeChart({ data, tf, hoverIdx, setHoverIdx, avgEdge }: {
  data: { t: string; basis: number; edge: number }[]
  tf: Timeframe
  hoverIdx: number | null
  setHoverIdx: (i: number | null) => void
  avgEdge: number
}) {
  const W = 700
  const H = 140
  const padTop = 14
  const padBottom = 6
  const barW = Math.max(1.5, (W / data.length) - 0.5)

  const maxEdge = Math.max(...data.map((d) => d.edge), 0.001)
  const chartH = H - padTop - padBottom

  const labelCount = 7
  const labelStep = Math.max(1, Math.floor(data.length / labelCount))

  const yTicks = [0, 0.25, 0.5, 0.75, 1]

  const avgY = padTop + chartH - (avgEdge / maxEdge) * chartH

  const [animKey, setAnimKey] = useState(0)
  const prevTf = useRef(tf)
  const prevLen = useRef(data.length)

  useEffect(() => {
    if (tf !== prevTf.current || data.length !== prevLen.current) {
      setAnimKey((k) => k + 1)
      prevTf.current = tf
      prevLen.current = data.length
    }
  }, [tf, data.length])

  return (
    <svg
      key={animKey}
      viewBox={`0 0 ${W} ${H + 18}`}
      className="w-full"
      onMouseLeave={() => setHoverIdx(null)}
    >
      <defs>
        <linearGradient id="edgeGrad" x1="0" y1="1" x2="0" y2="0">
          <stop offset="0%" stopColor="#22c55e" stopOpacity="0.08" />
          <stop offset="40%" stopColor="#22c55e" stopOpacity="0.4" />
          <stop offset="100%" stopColor="#22c55e" stopOpacity="0.9" />
        </linearGradient>
      </defs>

      {/* Background */}
      <rect x="0" y={padTop} width={W} height={chartH} fill="#22c55e" opacity="0.015" rx="2" />

      {/* Y grid + labels */}
      {yTicks.map((t) => {
        const y = padTop + chartH - t * chartH
        return (
          <g key={t}>
            {t > 0 && <line x1="0" y1={y} x2={W} y2={y} stroke="rgba(255,255,255,0.03)" strokeWidth="1" />}
            <text x={W - 2} y={y - 3} fill="#64748b" fontSize="7" fontFamily="monospace" textAnchor="end" opacity="0.5">
              {fmtPct(t * maxEdge)}
            </text>
          </g>
        )
      })}

      {/* Zero line */}
      <line x1="0" y1={padTop + chartH} x2={W} y2={padTop + chartH} stroke="rgba(255,255,255,0.06)" strokeWidth="1" />

      {/* Average line */}
      <line x1="0" y1={avgY} x2={W} y2={avgY} stroke="#3b82f6" strokeWidth="1" strokeDasharray="4,3">
        <animate attributeName="opacity" from="0" to="0.3" dur="0.6s" begin="0.4s" fill="freeze" />
      </line>

      {/* Bars */}
      {data.map((d, i) => {
        const x = (i / data.length) * W
        const barH = (d.edge / maxEdge) * chartH
        const baseY = padTop + chartH
        const targetY = padTop + chartH - barH
        const delay = (i / data.length) * 0.4

        return (
          <g key={i}>
            <rect
              x={x}
              width={barW}
              fill="url(#edgeGrad)"
              opacity={hoverIdx === i ? 1 : 0.85}
              y={baseY}
              height={0}
            >
              <animate attributeName="y" from={baseY} to={targetY} dur="0.5s" begin={`${delay}s`} fill="freeze" calcMode="spline" keySplines="0.25 0.1 0.25 1" keyTimes="0;1" />
              <animate attributeName="height" from="0" to={Math.max(0.5, barH)} dur="0.5s" begin={`${delay}s`} fill="freeze" calcMode="spline" keySplines="0.25 0.1 0.25 1" keyTimes="0;1" />
            </rect>
            <rect
              x={x}
              y={0}
              width={Math.max(barW, 4)}
              height={H}
              fill="transparent"
              onMouseEnter={() => setHoverIdx(i)}
            />
          </g>
        )
      })}

      {/* Hover crosshair */}
      {hoverIdx !== null && (
        <line
          x1={(hoverIdx / data.length) * W + barW / 2}
          y1={0}
          x2={(hoverIdx / data.length) * W + barW / 2}
          y2={H}
          stroke="rgba(255,255,255,0.12)"
          strokeWidth="1"
          strokeDasharray="2,2"
        />
      )}

      {/* X-axis labels */}
      {data.map((d, i) => {
        if (i % labelStep !== 0) return null
        const x = (i / data.length) * W + barW / 2
        return (
          <text key={i} x={Math.max(18, x)} y={H + 12} textAnchor="middle" fill="#64748b" fontSize="7" fontFamily="monospace">
            <animate attributeName="opacity" from="0" to="1" dur="0.3s" begin="0.5s" fill="freeze" />
            {formatTime(d.t, tf)}
          </text>
        )
      })}
    </svg>
  )
}
