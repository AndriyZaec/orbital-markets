import { useEffect, useMemo, useState } from 'react'
import { getWeeklyAPR, type WeeklyAPRReport, type WeeklyAPRRow } from '../api'

type SortMetric = 'peak' | 'average'

export function WeeklyAPRPage() {
  const [data, setData] = useState<WeeklyAPRReport | null>(null)
  const [sortMetric, setSortMetric] = useState<SortMetric>('peak')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    getWeeklyAPR(controller.signal)
      .then((value) => { setData(value); setError(null) })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Unable to load weekly APR records.')
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [])

  const rows = useMemo(() => weeklyTopFive(data?.rows ?? [], sortMetric), [data, sortMetric])

  return (
    <div className="feature-page">
      <div className="feature-heading">
        <div>
          <span className="panel-kicker">Funding intelligence</span>
          <h2>Weekly APR records</h2>
          <p>Top five funding spreads per UTC calendar week, calculated from hourly venue snapshots.</p>
        </div>
        {data && <span className="count-badge">Updated {new Date(data.generated_at).toLocaleString()}</span>}
      </div>

      <div className="apr-toolbar">
        <div>
          <span>Rank each week by</span>
          <div className="sort-toggle" role="group" aria-label="Weekly APR ranking metric">
            <button type="button" aria-pressed={sortMetric === 'peak'} className={sortMetric === 'peak' ? 'active' : ''} onClick={() => setSortMetric('peak')}>Peak APR</button>
            <button type="button" aria-pressed={sortMetric === 'average'} className={sortMetric === 'average' ? 'active' : ''} onClick={() => setSortMetric('average')}>7d Avg APR</button>
          </div>
        </div>
        <p>Direction is captured at the peak APR record. The weekly average preserves that fixed direction.</p>
      </div>

      {error && <p className="inline-error">{error}</p>}
      <div className="table-wrap apr-table">
        <table>
          <thead>
            <tr>
              <th>Date</th>
              <th>#</th>
              <th>Ticker</th>
              <th>Venue long</th>
              <th>Venue short</th>
              <th className="numeric">Max APR record</th>
              <th className="numeric">Weekly average APR</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ row, rank }) => (
              <tr key={`${row.week_start}-${row.ticker}-${row.venue_long}-${row.venue_short}`}>
                <td className="week-cell">{formatWeek(row.week_start)}</td>
                <td className="rank-cell">{rank}</td>
                <td className="ticker-cell">{row.ticker}</td>
                <td><Venue value={row.venue_long} side="long" /></td>
                <td><Venue value={row.venue_short} side="short" /></td>
                <APRCell value={row.max_apr} emphasized={sortMetric === 'peak'} />
                <APRCell value={row.weekly_average_apr} emphasized={sortMetric === 'average'} />
              </tr>
            ))}
          </tbody>
        </table>
        {loading && <div className="table-state">Loading weekly APR records...</div>}
        {!loading && !error && rows.length === 0 && <div className="table-state">No hourly funding records are available yet.</div>}
      </div>
    </div>
  )
}

function weeklyTopFive(rows: WeeklyAPRRow[], metric: SortMetric): Array<{ row: WeeklyAPRRow; rank: number }> {
  const weeks = new Map<string, WeeklyAPRRow[]>()
  for (const row of rows) {
    const week = weeks.get(row.week_start) ?? []
    week.push(row)
    weeks.set(row.week_start, week)
  }

  return [...weeks.entries()]
    .sort(([left], [right]) => right.localeCompare(left))
    .flatMap(([, weekRows]) => weekRows
      .sort((left, right) => metricValue(right, metric) - metricValue(left, metric)
        || right.max_apr - left.max_apr
        || left.ticker.localeCompare(right.ticker))
      .slice(0, 5)
      .map((row, index) => ({ row, rank: index + 1 })))
}

function metricValue(row: WeeklyAPRRow, metric: SortMetric): number {
  return metric === 'peak' ? row.max_apr : row.weekly_average_apr
}

function formatWeek(value: string): string {
  const start = new Date(`${value}T00:00:00Z`)
  const end = new Date(start)
  end.setUTCDate(end.getUTCDate() + 6)
  const startLabel = start.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })
  const endLabel = end.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })
  return `${startLabel} - ${endLabel}`
}

function Venue({ value, side }: { value: string; side: 'long' | 'short' }) {
  return <span className={`venue-pill venue-${side}`}><span />{venueLabel(value)}</span>
}

function venueLabel(value: string): string {
  return value.toLowerCase() === 'hyperliquid' ? 'Hyperliquid' : value.charAt(0).toUpperCase() + value.slice(1)
}

function APRCell({ value, emphasized }: { value: number; emphasized: boolean }) {
  const tone = value < 0 ? 'negative' : value > 0 ? 'positive' : ''
  return <td className={`numeric apr-value ${tone}${emphasized ? ' emphasized' : ''}`}>{formatAPR(value)}</td>
}

function formatAPR(value: number): string {
  const percent = value * 100
  return `${percent > 0 ? '+' : ''}${percent.toFixed(2)}%`
}
