import { useEffect, useMemo, useState } from 'react'
import { getWeeklyAPR, type WeeklyAPRReport, type WeeklyAPRRow } from '../api'

type SortMetric = 'peak' | 'average'
type SortDirection = 'asc' | 'desc'

interface RankedRow {
  row: WeeklyAPRRow
  rank: number
}

interface WeeklyGroup {
  weekStart: string
  rows: WeeklyAPRRow[]
}

export function WeeklyAPRPage() {
  const [data, setData] = useState<WeeklyAPRReport | null>(null)
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

  const weeks = useMemo(() => groupWeeklyRows(data?.rows ?? []), [data])

  return (
    <div className="feature-page">
      <div className="feature-heading">
        <div>
          <span className="panel-kicker">Funding intelligence</span>
          <h2>Weekly APR records</h2>
          <p>Top five funding spreads for each UTC week. The current week is week-to-date; averages preserve the peak direction.</p>
        </div>
        {loading
          ? <span className="loading-badge" role="status"><span className="mini-loader" />Loading</span>
          : data && <span className="count-badge">Updated {new Date(data.generated_at).toLocaleString()}</span>}
      </div>

      {error && <p className="inline-error">{error}</p>}
      {!loading && !error && weeks.length === 0 && <div className="table-wrap"><div className="table-state">No hourly funding records are available yet.</div></div>}

      <div className="apr-weeks">
        {weeks.map((week) => (
          <WeeklyTable key={week.weekStart} week={week} />
        ))}
      </div>
    </div>
  )
}

export function groupWeeklyRows(rows: WeeklyAPRRow[]): WeeklyGroup[] {
  const weeks = new Map<string, WeeklyAPRRow[]>()
  for (const row of rows) {
    const week = weeks.get(row.week_start) ?? []
    week.push(row)
    weeks.set(row.week_start, week)
  }

  return [...weeks.entries()]
    .sort(([left], [right]) => right.localeCompare(left))
    .map(([weekStart, weekRows]) => ({ weekStart, rows: weekRows }))
}

export function rankWeeklyRows(rows: WeeklyAPRRow[], metric: SortMetric, direction: SortDirection): RankedRow[] {
  const ranked = [...rows]
    .sort((left, right) => metricValue(right, metric) - metricValue(left, metric)
      || right.max_apr - left.max_apr
      || left.ticker.localeCompare(right.ticker))
    .slice(0, 5)
    .map((row, index) => ({ row, rank: index + 1 }))
  return direction === 'desc' ? ranked : ranked.reverse()
}

function metricValue(row: WeeklyAPRRow, metric: SortMetric): number {
  return metric === 'peak' ? row.max_apr : row.weekly_average_apr
}

function WeeklyTable({ week }: { week: WeeklyGroup }) {
  const [sortMetric, setSortMetric] = useState<SortMetric>('peak')
  const [sortDirection, setSortDirection] = useState<SortDirection>('desc')
  const rows = useMemo(() => rankWeeklyRows(week.rows, sortMetric, sortDirection), [sortDirection, sortMetric, week.rows])
  const headingID = `week-${week.weekStart}`

  const handleSort = (metric: SortMetric) => {
    if (metric === sortMetric) {
      setSortDirection((direction) => direction === 'desc' ? 'asc' : 'desc')
      return
    }
    setSortMetric(metric)
    setSortDirection('desc')
  }

  return (
    <section className="apr-week">
      <div className="apr-week-heading">
        <span>UTC week</span>
        <h3 id={headingID}>{formatWeek(week.weekStart)}</h3>
      </div>
      <div className="table-wrap apr-table">
        <table aria-labelledby={headingID}>
          <thead>
            <tr>
              <th>#</th>
              <th>Ticker</th>
              <SortHeader metric="peak" label="Max APR record" current={sortMetric} direction={sortDirection} onSort={handleSort} />
              <SortHeader metric="average" label="Week avg APR" current={sortMetric} direction={sortDirection} onSort={handleSort} />
              <th>Venue long</th>
              <th>Venue short</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ row, rank }) => (
              <tr key={`${row.ticker}-${row.venue_long}-${row.venue_short}`}>
                <td className="rank-cell">{rank}</td>
                <td className="ticker-cell">{row.ticker}</td>
                <APRCell value={row.max_apr} emphasized={sortMetric === 'peak'} />
                <APRCell value={row.weekly_average_apr} emphasized={sortMetric === 'average'} />
                <td><Venue value={row.venue_long} side="long" /></td>
                <td><Venue value={row.venue_short} side="short" /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function SortHeader({ metric, label, current, direction, onSort }: {
  metric: SortMetric
  label: string
  current: SortMetric
  direction: SortDirection
  onSort: (metric: SortMetric) => void
}) {
  const active = metric === current
  return (
    <th className={`numeric sortable-header${active ? ' active' : ''}`} aria-sort={active ? (direction === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button type="button" onClick={() => onSort(metric)}>
        {label}
        <SortIndicator active={active} direction={direction} />
      </button>
    </th>
  )
}

function SortIndicator({ active, direction }: { active: boolean; direction: SortDirection }) {
  return active ? (
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none" aria-hidden="true">
      <path d={direction === 'desc' ? 'M2 4l3 3 3-3' : 'M2 6l3-3 3 3'} stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  ) : (
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none" aria-hidden="true" className="inactive-sort">
      <path d="M3 4l2-2 2 2M3 6l2 2 2-2" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function formatWeek(value: string): string {
  const start = new Date(`${value}T00:00:00Z`)
  const end = new Date(start)
  end.setUTCDate(end.getUTCDate() + 6)
  const startLabel = start.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })
  const endLabel = end.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
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
