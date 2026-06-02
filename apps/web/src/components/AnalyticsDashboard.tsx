import { useAnalytics } from '@/hooks/useAnalytics'
import { usePaperPositions } from '@/hooks/usePaperPositions'

function fmtUsd(n: number) {
  const abs = Math.abs(n)
  const sign = n < 0 ? '-' : ''
  if (abs >= 1_000_000) return sign + '$' + (abs / 1_000_000).toFixed(2) + 'M'
  if (abs >= 1_000) return sign + '$' + (abs / 1_000).toFixed(2) + 'K'
  return sign + '$' + abs.toFixed(2)
}

function fmtHours(h: number) {
  if (h >= 24) return Math.floor(h / 24) + 'd ' + Math.floor(h % 24) + 'h'
  return Math.floor(h) + 'h'
}

function pnlColor(n: number) {
  if (n > 0) return 'text-green-400'
  if (n < 0) return 'text-red-400'
  return 'text-muted-foreground'
}

function riskLabel(risk: string) {
  const cfg: Record<string, { text: string; color: string }> = {
    safe: { text: 'Safe', color: 'text-green-400' },
    elevated: { text: 'Elevated', color: 'text-yellow-400' },
    warning: { text: 'Warning', color: 'text-orange-400' },
    critical: { text: 'Critical', color: 'text-red-400' },
  }
  return cfg[risk] ?? { text: risk || 'N/A', color: 'text-muted-foreground' }
}

export function AnalyticsDashboard() {
  const { data, loading, error } = useAnalytics()
  const { positions } = usePaperPositions()

  const openPositions = positions.filter((p) => p.state === 'open' || p.state === 'degraded')
  const degradedPositions = positions.filter((p) => p.state === 'degraded')

  const grossExposure = openPositions.reduce((s, p) => s + p.target_notional, 0)
  const avgLeverage = openPositions.length > 0
    ? openPositions.reduce((s, p) => s + (p.leverage?.leverage || p.leverage?.effective_leverage || 1), 0) / openPositions.length
    : 0
  const marginInUse = avgLeverage > 0 ? grossExposure / avgLeverage : 0

  const totalFunding = openPositions.reduce((s, p) => s + p.funding_pnl, 0)
  const totalPnl = openPositions.reduce((s, p) => s + p.total_pnl, 0)

  const worstPosition = openPositions.length > 0
    ? [...openPositions].sort((a, b) => {
        const riskOrder = { critical: 0, warning: 1, elevated: 2, safe: 3, '': 4 }
        const ra = riskOrder[(a.leg_1_fill?.liq_risk ?? '') as keyof typeof riskOrder] ?? 4
        const rb = riskOrder[(b.leg_1_fill?.liq_risk ?? '') as keyof typeof riskOrder] ?? 4
        return ra - rb
      })[0]
    : null

  const largestPosition = openPositions.length > 0
    ? [...openPositions].sort((a, b) => b.target_notional - a.target_notional)[0]
    : null

  const summary = data?.summary
  const healthStatus = degradedPositions.length > 0 ? 'Degraded' : openPositions.length > 0 ? 'Healthy' : 'Idle'
  const healthColor = degradedPositions.length > 0 ? 'text-orange-400' : openPositions.length > 0 ? 'text-green-400' : 'text-muted-foreground'
  const healthDot = degradedPositions.length > 0 ? 'bg-orange-400' : openPositions.length > 0 ? 'bg-green-400' : 'bg-zinc-500'

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-muted-foreground">Loading...</div>
    )
  }

  if (error) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-muted-foreground">Error: {error}</div>
    )
  }

  return (
    <div className="max-w-4xl mx-auto px-5 py-8">
      {/* Header */}
      <div className="mb-6">
        <div className="flex items-center gap-3 mb-1">
          <h1 className="text-xl font-semibold text-foreground">Account Overview</h1>
          <div className="flex items-center gap-1.5">
            <div className={`size-2 rounded-full ${healthDot}`} />
            <span className={`text-xs font-medium ${healthColor}`}>{healthStatus}</span>
          </div>
        </div>
        <p className="text-sm text-muted-foreground">Portfolio state and risk summary across active positions.</p>
      </div>

      {/* Summary Cards */}
      <div className="grid grid-cols-3 lg:grid-cols-6 gap-3 mb-8">
        <MetricCard label="Open Positions" value={String(openPositions.length)} />
        <MetricCard label="Gross Exposure" value={fmtUsd(grossExposure)} />
        <MetricCard label="Margin In Use" value={fmtUsd(marginInUse)} />
        <MetricCard label="Avg Leverage" value={avgLeverage > 0 ? avgLeverage.toFixed(1) + 'x' : '--'} />
        <MetricCard label="Unrealized PnL" value={fmtUsd(totalPnl)} valueClass={pnlColor(totalPnl)} />
        <MetricCard label="Funding Earned" value={fmtUsd(totalFunding)} valueClass={pnlColor(totalFunding)} />
      </div>

      <div className="grid grid-cols-3 gap-6 mb-8">
        {/* Exposure Snapshot */}
        <div>
          <h2 className="text-sm font-semibold text-foreground mb-3">Exposure</h2>
          <div className="rounded-lg border border-white/[0.06] bg-gradient-to-b from-white/[0.03] to-transparent px-4 py-3.5">
            <Row label="Gross Exposure" value={fmtUsd(grossExposure)} />
            <Row label="Avg Leverage" value={avgLeverage > 0 ? avgLeverage.toFixed(1) + 'x' : '--'} />
            <Row label="Largest Position" value={largestPosition ? `${largestPosition.asset} · ${fmtUsd(largestPosition.target_notional)}` : '--'} />
            <Row label="Active Venues" value={
              openPositions.length > 0
                ? [...new Set(openPositions.flatMap((p) => [p.venue_pair.venue_a, p.venue_pair.venue_b]))].length + ' venues'
                : '--'
            } last />
          </div>
        </div>

        {/* Risk Snapshot */}
        <div>
          <h2 className="text-sm font-semibold text-foreground mb-3">Risk</h2>
          <div className="rounded-lg border border-white/[0.06] bg-gradient-to-b from-white/[0.03] to-transparent px-4 py-3.5">
            <Row label="Degraded Positions" value={String(degradedPositions.length)} valueClass={degradedPositions.length > 0 ? 'text-orange-400' : undefined} />
            <Row label="Highest Risk" value={
              worstPosition
                ? `${worstPosition.asset}`
                : '--'
            } extra={worstPosition?.leg_1_fill?.liq_risk ? (
              <span className={`text-[10px] font-medium ${riskLabel(worstPosition.leg_1_fill.liq_risk).color}`}>
                {riskLabel(worstPosition.leg_1_fill.liq_risk).text}
              </span>
            ) : undefined} />
            <Row label="Failed Trades" value={String(summary?.failed_trades ?? 0)} valueClass={(summary?.failed_trades ?? 0) > 0 ? 'text-red-400' : undefined} />
            <Row label="Account Health" value={healthStatus} valueClass={healthColor} last />
          </div>
        </div>

        {/* Carry Snapshot */}
        <div>
          <h2 className="text-sm font-semibold text-foreground mb-3">Carry & Funding</h2>
          <div className="rounded-lg border border-white/[0.06] bg-gradient-to-b from-white/[0.03] to-transparent px-4 py-3.5">
            <Row label="Funding PnL" value={fmtUsd(totalFunding)} valueClass={pnlColor(totalFunding)} />
            <Row label="Price PnL" value={fmtUsd(openPositions.reduce((s, p) => s + p.price_pnl, 0))} valueClass={pnlColor(openPositions.reduce((s, p) => s + p.price_pnl, 0))} />
            <Row label="Active Carry Positions" value={String(openPositions.length)} />
            <Row label="Avg Hold Time" value={summary?.avg_hold_hours ? fmtHours(summary.avg_hold_hours) : '--'} last />
          </div>
        </div>
      </div>

      {/* Recent Activity */}
      {positions.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold text-foreground mb-3">Recent Activity</h2>
          <div className="rounded-lg border border-white/[0.06] overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="bg-white/[0.03] text-muted-foreground">
                  <th className="text-left px-4 py-2 font-medium">Asset</th>
                  <th className="text-left px-4 py-2 font-medium">State</th>
                  <th className="text-right px-4 py-2 font-medium">Size</th>
                  <th className="text-right px-4 py-2 font-medium">PnL</th>
                  <th className="text-right px-4 py-2 font-medium">When</th>
                </tr>
              </thead>
              <tbody>
                {positions.slice(0, 6).map((p) => {
                  const stateColor = p.state === 'open' ? 'text-green-400' : p.state === 'degraded' ? 'text-orange-400' : p.state === 'closed' ? 'text-muted-foreground' : 'text-red-400'
                  const age = Date.now() - new Date(p.created_at).getTime()
                  const ageStr = age < 3_600_000 ? Math.floor(age / 60_000) + 'm ago' : age < 86_400_000 ? Math.floor(age / 3_600_000) + 'h ago' : Math.floor(age / 86_400_000) + 'd ago'
                  return (
                    <tr key={p.id} className="border-t border-border">
                      <td className="px-4 py-2 font-medium text-foreground">{p.asset}</td>
                      <td className={`px-4 py-2 capitalize ${stateColor}`}>{p.state}</td>
                      <td className="px-4 py-2 text-right font-mono text-muted-foreground">{fmtUsd(p.target_notional)}</td>
                      <td className={`px-4 py-2 text-right font-mono ${pnlColor(p.total_pnl)}`}>{fmtUsd(p.total_pnl)}</td>
                      <td className="px-4 py-2 text-right text-muted-foreground/60">{ageStr}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}

function MetricCard({ label, value, valueClass = 'text-foreground' }: { label: string; value: string; valueClass?: string }) {
  return (
    <div className="bg-gradient-to-b from-white/[0.04] to-white/[0.02] border border-white/[0.06] rounded-lg px-4 py-3">
      <p className="text-[10px] text-muted-foreground/70 mb-1">{label}</p>
      <p className={`text-base font-mono font-semibold ${valueClass}`}>{value}</p>
    </div>
  )
}

function Row({ label, value, valueClass, extra, last }: { label: string; value: string; valueClass?: string; extra?: React.ReactNode; last?: boolean }) {
  return (
    <div className={`flex items-center justify-between py-2 ${last ? '' : 'border-b border-border'}`}>
      <span className="text-[11px] text-muted-foreground">{label}</span>
      <div className="flex items-center gap-2">
        {extra}
        <span className={`text-xs font-mono font-medium ${valueClass ?? 'text-foreground'}`}>{value}</span>
      </div>
    </div>
  )
}
