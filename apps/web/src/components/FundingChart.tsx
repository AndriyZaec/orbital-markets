import { useState, useMemo } from 'react'

interface Props {
  asset: string
  currentSpread: number
}

type Timeframe = 'D' | 'W' | 'M'

function generateMockData(asset: string, timeframe: Timeframe, currentSpread: number) {
  const seed = Array.from(asset).reduce((a, c) => a + c.charCodeAt(0), 0)
  const rng = (i: number) => {
    const x = Math.sin(seed * 9301 + i * 49297) * 49297
    return x - Math.floor(x)
  }

  const count = timeframe === 'D' ? 24 : timeframe === 'W' ? 7 * 4 : 30
  const now = Date.now()
  const interval = timeframe === 'D' ? 3600_000 : timeframe === 'W' ? 6 * 3600_000 : 24 * 3600_000

  const points: { time: number; spread: number; annualized: number }[] = []
  for (let i = 0; i < count; i++) {
    const drift = (rng(i) - 0.45) * currentSpread * 3
    const spread = currentSpread + drift * (1 - i / count * 0.3)
    const annualized = spread * 8760
    points.push({
      time: now - (count - 1 - i) * interval,
      spread,
      annualized,
    })
  }
  return points
}

function formatTimeLabel(ts: number, tf: Timeframe) {
  const d = new Date(ts)
  if (tf === 'D') return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

export function FundingChart({ asset, currentSpread }: Props) {
  const [timeframe, setTimeframe] = useState<Timeframe>('W')
  const [hoveredIdx, setHoveredIdx] = useState<number | null>(null)

  const data = useMemo(
    () => generateMockData(asset, timeframe, currentSpread),
    [asset, timeframe, currentSpread]
  )

  const maxAbs = Math.max(...data.map((d) => Math.abs(d.spread)), Math.abs(currentSpread) * 0.5)
  const chartW = 600
  const chartH = 140
  const barW = Math.max(2, (chartW / data.length) - 2)
  const midY = chartH / 2

  const labelCount = 6
  const labelStep = Math.max(1, Math.floor(data.length / labelCount))

  const hovered = hoveredIdx !== null ? data[hoveredIdx] : null

  return (
    <div>
      {/* Header */}
      <div className="flex items-center justify-between mb-3">
        <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">
          Historical Spread
        </p>
        <div className="flex gap-0.5 bg-white/[0.04] rounded-md p-0.5">
          {(['D', 'W', 'M'] as Timeframe[]).map((tf) => (
            <button
              key={tf}
              onClick={() => setTimeframe(tf)}
              className={`px-2 py-0.5 rounded text-[11px] font-medium transition-colors ${
                timeframe === tf
                  ? 'bg-white/[0.08] text-foreground'
                  : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {tf}
            </button>
          ))}
        </div>
      </div>

      {/* Tooltip */}
      <div className="h-5 mb-1">
        {hovered ? (
          <div className="flex items-center gap-4 text-[11px] font-mono">
            <span className="text-muted-foreground">
              {formatTimeLabel(hovered.time, timeframe)}
            </span>
            <span className="text-muted-foreground">
              1h Spread{' '}
              <span className={hovered.spread >= 0 ? 'text-green-400' : 'text-red-400'}>
                {(hovered.spread * 100).toFixed(4)}%
              </span>
            </span>
            <span className="text-muted-foreground">
              Annualized{' '}
              <span className={hovered.annualized >= 0 ? 'text-green-400' : 'text-red-400'}>
                {(hovered.annualized * 100).toFixed(2)}%
              </span>
            </span>
          </div>
        ) : (
          <div className="flex items-center gap-3 text-[11px] text-muted-foreground">
            <span>1h Spread / Annualized</span>
          </div>
        )}
      </div>

      {/* Chart */}
      <div className="relative">
        <svg
          viewBox={`0 0 ${chartW} ${chartH + 20}`}
          className="w-full"
          onMouseLeave={() => setHoveredIdx(null)}
        >
          {/* Zero line */}
          <line x1="0" y1={midY} x2={chartW} y2={midY} stroke="rgba(255,255,255,0.06)" strokeWidth="1" />

          {/* Y-axis labels */}
          <text x="2" y={12} className="fill-[#64748b] text-[9px]" fontFamily="monospace">
            {(maxAbs * 100).toFixed(3)}%
          </text>
          <text x="2" y={chartH - 2} className="fill-[#64748b] text-[9px]" fontFamily="monospace">
            -{(maxAbs * 100).toFixed(3)}%
          </text>

          {/* Bars */}
          {data.map((d, i) => {
            const x = (i / data.length) * chartW + 1
            const normalizedH = (Math.abs(d.spread) / maxAbs) * (midY - 8)
            const isPositive = d.spread >= 0
            const y = isPositive ? midY - normalizedH : midY
            const isHovered = hoveredIdx === i

            return (
              <g key={i}>
                <rect
                  x={x}
                  y={y}
                  width={barW}
                  height={Math.max(1, normalizedH)}
                  fill={isPositive ? (isHovered ? '#22c55e' : '#22c55e99') : (isHovered ? '#ef4444' : '#ef444499')}
                  rx={1}
                />
                {/* Hover target — wider than the bar */}
                <rect
                  x={x - 1}
                  y={0}
                  width={barW + 2}
                  height={chartH}
                  fill="transparent"
                  onMouseEnter={() => setHoveredIdx(i)}
                />
              </g>
            )
          })}

          {/* X-axis labels */}
          {data.map((d, i) => {
            if (i % labelStep !== 0) return null
            const x = (i / data.length) * chartW + barW / 2
            return (
              <text
                key={i}
                x={x}
                y={chartH + 14}
                textAnchor="middle"
                className="fill-[#64748b] text-[8px]"
                fontFamily="monospace"
              >
                {formatTimeLabel(d.time, timeframe)}
              </text>
            )
          })}
        </svg>
      </div>
    </div>
  )
}
