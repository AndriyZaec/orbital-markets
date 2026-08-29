import { useEffect, useState } from 'react'
import { getLiveAnalytics, type LiveAnalytics, type VolumeBreakdown, type VolumeWindow } from '../api'

export function AnalyticsPage() {
  const [data, setData] = useState<LiveAnalytics | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    getLiveAnalytics(controller.signal).then((value) => { setData(value); setError(null) }).catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Unable to load live analytics.') }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [refresh])
  if (loading && !data) return <div className="table-state">Loading live operations metrics...</div>
  if (error && !data) return <div className="error-panel"><p>{error}</p><button type="button" onClick={() => setRefresh((value) => value + 1)}>Retry</button></div>
  if (!data) return null
  return <div className="feature-page"><div className="feature-heading"><div><span className="panel-kicker">Read-only operations</span><h2>Live analytics</h2><p>Execution volume and live trading health. No wallet-level records are exposed.</p></div><span className="count-badge">Updated {new Date(data.generated_at).toLocaleString()}</span></div><div className="analytics-cards"><VolumeCard label="All time" value={data.volume.all_time} /><VolumeCard label="Last 7 days" value={data.volume.last_7d} /><VolumeCard label="Last 24 hours" value={data.volume.last_24h} /></div><div className="analytics-columns"><Breakdown title="By venue" rows={data.volume.by_venue} /><Breakdown title="By asset" rows={data.volume.by_asset} /></div><div><h3 className="section-label">Trade health</h3><div className="health-grid"><HealthMetric label="Open positions" value={data.trades.open_positions} /><HealthMetric label="Degraded" value={data.trades.degraded_positions} warn={data.trades.degraded_positions > 0} /><HealthMetric label="Successful opens" value={data.trades.successful_opens} /><HealthMetric label="Failed opens" value={data.trades.failed_opens} warn={data.trades.failed_opens > 0} /><HealthMetric label="Closed trades" value={data.trades.closed_trades} /></div></div></div>
}

function VolumeCard({ label, value }: { label: string; value: VolumeWindow }) { return <div className="metric-card"><span>{label}</span><strong>{formatUsd(value.gross_venue_volume)}</strong><small>Gross venue volume</small><div className="metric-rows"><p>Hedged <b>{formatUsd(value.hedged_trade_volume)}</b></p><p>Open <b>{formatUsd(value.open_volume)}</b></p><p>Close <b>{formatUsd(value.close_volume)}</b></p></div></div> }
function Breakdown({ title, rows }: { title: string; rows: VolumeBreakdown[] }) { return <div><h3 className="section-label">{title}</h3><div className="breakdown-list">{rows.length === 0 ? <p className="muted">No executed volume yet.</p> : rows.map((row) => <div key={row.key}><strong>{row.key.replaceAll('_', ' ')}</strong><span>{formatUsd(row.gross_venue_volume)}</span></div>)}</div></div> }
function HealthMetric({ label, value, warn = false }: { label: string; value: number; warn?: boolean }) { return <div className="health-card"><span>{label}</span><strong className={warn ? 'warning-text' : ''}>{value}</strong></div> }
function formatUsd(value: number): string { const abs = Math.abs(value); if (abs >= 1_000_000) return `$${(value / 1_000_000).toFixed(2)}M`; if (abs >= 1_000) return `$${(value / 1_000).toFixed(2)}K`; return `$${value.toFixed(2)}` }
