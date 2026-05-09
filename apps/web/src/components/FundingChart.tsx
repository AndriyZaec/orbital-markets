import { useState } from 'react'
import { useHistory } from '@/hooks/useHistory'

interface Props {
  asset: string
  venueA: string
  venueB: string
}

type Timeframe = 'D' | 'W' | 'M'

const rangeMap: Record<Timeframe, string> = { D: '24h', W: '7d', M: '30d' }

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

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <div>
          <p className="text-xs font-medium text-foreground">Historical Edge</p>
          {hovered ? (
            <p className="text-[11px] font-mono text-muted-foreground mt-0.5">
              {formatTime(hovered.t, tf)}
              {'  '}
              <span className="text-green-400">{fmtPct(hovered.edge)}</span>
              <span className="text-muted-foreground/50"> ann.</span>
            </p>
          ) : (
            <p className="text-[11px] text-muted-foreground/50 mt-0.5">Annualized Gross Edge</p>
          )}
        </div>
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

      {loading ? (
        <div className="flex items-center justify-center h-[160px] text-xs text-muted-foreground">Loading...</div>
      ) : error || data.length === 0 ? (
        <div className="flex items-center justify-center h-[160px] text-xs text-muted-foreground">
          {error ? `Error: ${error}` : 'No data yet — snapshots accumulate every minute'}
        </div>
      ) : (
        <EdgeChart data={data} tf={tf} hoverIdx={hoverIdx} setHoverIdx={setHoverIdx} />
      )}

      <div className="mt-2 rounded border border-blue-500/10 bg-blue-500/[0.04] px-3 py-1.5 flex items-start gap-2">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" className="text-blue-400/50 shrink-0 mt-px">
          <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.2"/>
          <path d="M8 7v4M8 5.5v.01" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>
        </svg>
        <p className="text-[11px] text-blue-300/60 leading-relaxed">
          Shows opportunity magnitude over time. Trade direction is determined by current venue funding rates.
        </p>
      </div>
    </div>
  )
}

function EdgeChart({ data, tf, hoverIdx, setHoverIdx }: {
  data: { t: string; basis: number; edge: number }[]
  tf: Timeframe
  hoverIdx: number | null
  setHoverIdx: (i: number | null) => void
}) {
  const W = 700
  const H = 160
  const padTop = 14
  const padBottom = 6
  const barW = Math.max(1.5, (W / data.length) - 0.5)

  const maxEdge = Math.max(...data.map((d) => d.edge), 0.001)
  const chartH = H - padTop - padBottom

  const labelCount = 7
  const labelStep = Math.max(1, Math.floor(data.length / labelCount))

  const yTicks = [0, 0.25, 0.5, 0.75, 1]

  return (
    <svg
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

      {/* Bars */}
      {data.map((d, i) => {
        const x = (i / data.length) * W
        const barH = (d.edge / maxEdge) * chartH
        const y = padTop + chartH - barH

        return (
          <g key={i}>
            <rect
              x={x}
              y={y}
              width={barW}
              height={Math.max(0.5, barH)}
              fill="url(#edgeGrad)"
              opacity={hoverIdx === i ? 1 : 0.85}
            />
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
            {formatTime(d.t, tf)}
          </text>
        )
      })}
    </svg>
  )
}
