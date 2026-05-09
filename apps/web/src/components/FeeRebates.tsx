import { useState, useEffect, useMemo } from 'react'
import { usePaperPositions } from '@/hooks/usePaperPositions'

const MONTHLY_RATE = 0.0012
const HOURS_PER_MONTH = 720

function fmtUsd(v: number) {
  return '$' + v.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function fmtDuration(ms: number) {
  const h = Math.floor(ms / 3_600_000)
  const m = Math.floor((ms % 3_600_000) / 60_000)
  if (h >= 24) {
    const d = Math.floor(h / 24)
    return `${d}d ${h % 24}h`
  }
  return `${h}h ${m}m`
}

interface Accrual {
  asset: string
  notional: number
  openedAt: string
  durationMs: number
  rebate: number
}

function computeAccruals(positions: { asset: string; target_notional: number; opened_at: string | null; state: string }[], now: number): Accrual[] {
  return positions
    .filter((p) => p.state === 'open' && p.opened_at)
    .map((p) => {
      const durationMs = now - new Date(p.opened_at!).getTime()
      const hoursOpen = Math.max(0, durationMs / 3_600_000)
      const rebate = MONTHLY_RATE * p.target_notional * hoursOpen / HOURS_PER_MONTH
      return {
        asset: p.asset,
        notional: p.target_notional,
        openedAt: p.opened_at!,
        durationMs,
        rebate,
      }
    })
}

const TIERS = [
  { name: 'Explorer', rate: '0.12%', desc: 'Default tier for all traders', active: true, badge: 'Current' },
  { name: 'Partner', rate: '0.18%', desc: 'For volume traders and integrators', active: false, badge: 'Coming soon' },
  { name: 'Institutional', rate: 'Custom', desc: 'Tailored terms for large accounts', active: false, badge: 'Contact us' },
]

const BENEFITS = [
  { title: 'Offset Fees', desc: 'Rebates reduce your effective trading costs over time' },
  { title: 'Reward Duration', desc: 'Longer holds earn proportionally more rebates' },
  { title: 'Aligned Incentives', desc: 'We earn when you earn — rebates grow with your success' },
]

export function FeeRebates() {
  const { positions } = usePaperPositions()
  const [now, setNow] = useState(Date.now())

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  const accruals = useMemo(() => computeAccruals(positions, now), [positions, now])

  const totalEarned = accruals.reduce((s, a) => s + a.rebate, 0)
  const totalNotional = accruals.reduce((s, a) => s + a.notional, 0)
  const monthlyRate = MONTHLY_RATE * totalNotional
  const eligibleCount = accruals.length

  return (
    <div className="max-w-4xl mx-auto px-5 py-8">
      {/* Header */}
      <div className="mb-8">
        <h1 className="text-xl font-semibold text-foreground mb-1">Fee Rebates</h1>
        <p className="text-sm text-muted-foreground">
          Earn rebates on every spread trade. The longer you hold, the more you earn.
        </p>
      </div>

      {/* Summary Cards */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3 mb-8">
        <MetricCard label="Total Earned" value={fmtUsd(totalEarned)} valueClass="text-green-400" />
        <MetricCard label="Accrual Rate" value={fmtUsd(monthlyRate) + '/mo'} />
        <MetricCard label="Eligible Positions" value={String(eligibleCount)} />
        <MetricCard label="Projected Monthly" value={fmtUsd(monthlyRate)} valueClass="text-green-400/80" />
      </div>

      {/* Active Position Accruals */}
      <div className="mb-8">
        <h2 className="text-sm font-semibold text-foreground mb-3">Active Position Accruals</h2>
        {accruals.length === 0 ? (
          <div className="flex items-center justify-center h-32 rounded-lg border border-white/[0.06] bg-white/[0.02] text-sm text-muted-foreground">
            Open spread trades to start earning rebates
          </div>
        ) : (
          <div className="rounded-lg border border-white/[0.06] overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="bg-white/[0.03] text-muted-foreground text-xs">
                  <th className="text-left px-4 py-2.5 font-medium">Asset</th>
                  <th className="text-right px-4 py-2.5 font-medium">Notional</th>
                  <th className="text-right px-4 py-2.5 font-medium">Duration</th>
                  <th className="text-right px-4 py-2.5 font-medium">Accrued Rebate</th>
                </tr>
              </thead>
              <tbody>
                {accruals.map((a, i) => (
                  <tr key={i} className="border-t border-border hover:bg-white/[0.02] transition-colors">
                    <td className="px-4 py-2.5 font-medium text-foreground">{a.asset}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-muted-foreground">{fmtUsd(a.notional)}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-muted-foreground">{fmtDuration(a.durationMs)}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-green-400">{fmtUsd(a.rebate)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <p className="text-[11px] text-muted-foreground/50 mt-2">Projected from active positions</p>
      </div>

      {/* Rebate Tiers */}
      <div className="mb-8">
        <h2 className="text-sm font-semibold text-foreground mb-3">Rebate Tiers</h2>
        <div className="grid grid-cols-3 gap-3">
          {TIERS.map((tier) => (
            <div
              key={tier.name}
              className={`rounded-lg border px-4 py-4 ${
                tier.active
                  ? 'border-blue-500/20 bg-gradient-to-b from-blue-500/[0.08] to-blue-500/[0.02]'
                  : 'border-white/[0.06] bg-gradient-to-b from-white/[0.03] to-transparent'
              }`}
            >
              <div className="flex items-center gap-2 mb-2">
                <span className="text-sm font-semibold text-foreground">{tier.name}</span>
                <span
                  className={`text-[10px] px-1.5 py-0.5 rounded font-medium ${
                    tier.active
                      ? 'bg-blue-500/20 text-blue-400'
                      : 'bg-white/[0.06] text-muted-foreground'
                  }`}
                >
                  {tier.badge}
                </span>
              </div>
              <p className="text-lg font-mono font-semibold text-foreground mb-1">{tier.rate}</p>
              <p className="text-[11px] text-muted-foreground/60 leading-relaxed">{tier.desc}</p>
            </div>
          ))}
        </div>
        <p className="text-[11px] text-muted-foreground/50 mt-2">Estimated partner rebates</p>
      </div>

      {/* Why Rebates Matter */}
      <div>
        <h2 className="text-sm font-semibold text-foreground mb-3">Why Rebates Matter</h2>
        <div className="grid grid-cols-3 gap-3">
          {BENEFITS.map((b) => (
            <div key={b.title} className="rounded-lg border border-blue-500/10 bg-blue-500/[0.04] px-4 py-3">
              <p className="text-sm font-medium text-foreground mb-1">{b.title}</p>
              <p className="text-[11px] text-blue-300/60 leading-relaxed">{b.desc}</p>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function MetricCard({ label, value, valueClass = 'text-foreground' }: { label: string; value: string; valueClass?: string }) {
  return (
    <div className="bg-gradient-to-b from-white/[0.04] to-white/[0.02] border border-white/[0.06] rounded-lg px-5 py-4">
      <p className="text-[11px] text-muted-foreground/70 mb-1">{label}</p>
      <p className={`text-lg font-mono font-semibold ${valueClass}`}>{value}</p>
    </div>
  )
}
