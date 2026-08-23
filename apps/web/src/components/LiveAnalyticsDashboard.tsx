import { useState, type FormEvent } from 'react'
import { useLiveAnalytics, type LiveVolumeBreakdown, type LiveVolumeWindow } from '@/hooks/useLiveAnalytics'

function fmtUsd(value: number): string {
  const abs = Math.abs(value)
  const sign = value < 0 ? '-' : ''
  if (abs >= 1_000_000) return `${sign}$${(abs / 1_000_000).toFixed(2)}M`
  if (abs >= 1_000) return `${sign}$${(abs / 1_000).toFixed(2)}K`
  return `${sign}$${abs.toFixed(2)}`
}

function fmtDate(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '--' : date.toLocaleString()
}

export function LiveAnalyticsDashboard() {
  const [token, setToken] = useState('')
  const [tokenInput, setTokenInput] = useState('')
  const { data, loading, error, requiresToken } = useLiveAnalytics(token)

  function submitToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setToken(tokenInput.trim())
  }

  if (requiresToken && !data) {
    return (
      <div className="mx-auto flex min-h-full max-w-md items-center px-6 py-12">
        <form onSubmit={submitToken} className="w-full rounded-lg border border-white/[0.08] bg-white/[0.025] p-5">
          <p className="text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">Internal surface</p>
          <h1 className="mt-2 text-lg font-semibold text-foreground">Analytics access</h1>
          <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
            Enter the analytics access code. It is kept in memory for this page only.
          </p>
          <input
            type="password"
            value={tokenInput}
            onChange={(event) => setTokenInput(event.target.value)}
            autoComplete="current-password"
            className="mt-4 w-full rounded border border-border bg-black/20 px-3 py-2 text-sm text-foreground outline-none focus:border-cyan-400/50"
            placeholder="Access code"
          />
          <button
            type="submit"
            disabled={!tokenInput.trim()}
            className="mt-3 w-full rounded bg-blue-600 px-3 py-2 text-xs font-medium text-white transition-colors hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50"
          >
            Open analytics
          </button>
          {error && <p className="mt-3 text-xs text-red-400">{error}</p>}
        </form>
      </div>
    )
  }

  if (loading && !data) {
    return <div className="flex h-full items-center justify-center text-sm text-muted-foreground">Loading analytics...</div>
  }

  if (error && !data) {
    return <div className="flex h-full items-center justify-center text-sm text-red-400">{error}</div>
  }

  if (!data) return null

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 px-6 py-6">
      <header className="flex items-end justify-between gap-4">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-xl font-semibold text-foreground">Live Analytics</h1>
            <span className="size-1.5 rounded-full bg-green-400" />
          </div>
          <p className="mt-1 text-sm text-muted-foreground">Execution volume and live trading health.</p>
        </div>
        <p className="text-right text-[10px] text-muted-foreground/70">Updated {fmtDate(data.generated_at)}</p>
      </header>

      <div className="grid gap-3 md:grid-cols-3">
        <VolumeWindowCard label="All time" window={data.volume.all_time} />
        <VolumeWindowCard label="Last 7 days" window={data.volume.last_7d} />
        <VolumeWindowCard label="Last 24 hours" window={data.volume.last_24h} />
      </div>

      <div className="grid gap-6 lg:grid-cols-[1fr_1fr]">
        <Breakdown title="By venue" rows={data.volume.by_venue} />
        <Breakdown title="By asset" rows={data.volume.by_asset} />
      </div>

      <section>
        <h2 className="mb-3 text-sm font-semibold text-foreground">Trade health</h2>
        <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
          <Metric label="Open positions" value={data.trades.open_positions} />
          <Metric label="Degraded" value={data.trades.degraded_positions} valueClass={data.trades.degraded_positions > 0 ? 'text-orange-400' : undefined} />
          <Metric label="Successful opens" value={data.trades.successful_opens} />
          <Metric label="Failed opens" value={data.trades.failed_opens} valueClass={data.trades.failed_opens > 0 ? 'text-red-400' : undefined} />
          <Metric label="Closed trades" value={data.trades.closed_trades} />
        </div>
      </section>
    </div>
  )
}

function VolumeWindowCard({ label, window }: { label: string; window: LiveVolumeWindow }) {
  return (
    <section className="rounded-lg border border-white/[0.06] bg-gradient-to-b from-white/[0.04] to-white/[0.015] p-4">
      <p className="text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/70">{label}</p>
      <p className="mt-2 font-mono text-xl font-semibold text-foreground">{fmtUsd(window.gross_venue_volume)}</p>
      <p className="mt-1 text-[11px] text-muted-foreground">Gross venue volume</p>
      <div className="mt-4 space-y-2 border-t border-white/[0.06] pt-3">
        <Row label="Hedged volume" value={fmtUsd(window.hedged_trade_volume)} />
        <Row label="Open volume" value={fmtUsd(window.open_volume)} />
        <Row label="Close volume" value={fmtUsd(window.close_volume)} />
      </div>
    </section>
  )
}

function Breakdown({ title, rows }: { title: string; rows: LiveVolumeBreakdown[] }) {
  return (
    <section>
      <h2 className="mb-3 text-sm font-semibold text-foreground">{title}</h2>
      <div className="overflow-hidden rounded-lg border border-white/[0.06]">
        <table className="w-full text-xs">
          <thead className="bg-white/[0.03] text-muted-foreground">
            <tr>
              <th className="px-4 py-2 text-left font-medium">Name</th>
              <th className="px-4 py-2 text-right font-medium">Gross</th>
              <th className="px-4 py-2 text-right font-medium">Open</th>
              <th className="px-4 py-2 text-right font-medium">Close</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr><td colSpan={4} className="px-4 py-4 text-muted-foreground">No executed volume yet.</td></tr>
            ) : rows.map((row) => (
              <tr key={row.key} className="border-t border-border">
                <td className="px-4 py-2 font-medium capitalize text-foreground">{row.key.replaceAll('_', ' ')}</td>
                <td className="px-4 py-2 text-right font-mono text-foreground">{fmtUsd(row.gross_venue_volume)}</td>
                <td className="px-4 py-2 text-right font-mono text-muted-foreground">{fmtUsd(row.open_volume)}</td>
                <td className="px-4 py-2 text-right font-mono text-muted-foreground">{fmtUsd(row.close_volume)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between text-xs">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono text-foreground">{value}</span>
    </div>
  )
}

function Metric({ label, value, valueClass = 'text-foreground' }: { label: string; value: number; valueClass?: string }) {
  return (
    <div className="rounded-lg border border-white/[0.06] bg-white/[0.025] px-4 py-3">
      <p className="text-[10px] text-muted-foreground/70">{label}</p>
      <p className={`mt-1 font-mono text-base font-semibold ${valueClass}`}>{value}</p>
    </div>
  )
}
