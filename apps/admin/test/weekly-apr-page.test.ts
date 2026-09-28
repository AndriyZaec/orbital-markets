import { describe, expect, it } from 'vitest'
import type { WeeklyAPRRow } from '../src/api'
import { groupWeeklyRows, rankWeeklyRows } from '../src/features/WeeklyAPRPage'

function row(week: string, ticker: string, peak: number, average: number): WeeklyAPRRow {
  return {
    week_start: week,
    ticker,
    venue_long: 'hyperliquid',
    venue_short: 'aster',
    max_apr: peak,
    weekly_average_apr: average,
  }
}

describe('weekly APR table grouping', () => {
  const rows = [
    row('2026-09-21', 'A', 6, 1),
    row('2026-09-21', 'B', 5, 6),
    row('2026-09-21', 'C', 4, 2),
    row('2026-09-21', 'D', 3, 5),
    row('2026-09-21', 'E', 2, 3),
    row('2026-09-21', 'F', 1, 4),
    row('2026-09-14', 'OLDER', 10, 10),
  ]

  it('creates newest-first week tables and keeps only the selected metric top five', () => {
    const weeks = groupWeeklyRows(rows)

    expect(weeks.map((week) => week.weekStart)).toEqual(['2026-09-21', '2026-09-14'])
    const ranked = rankWeeklyRows(weeks[0].rows, 'average', 'desc')
    expect(ranked.map(({ row: value }) => value.ticker)).toEqual(['B', 'D', 'F', 'E', 'C'])
    expect(ranked.map(({ rank }) => rank)).toEqual([1, 2, 3, 4, 5])
  })

  it('reverses display order without changing metric ranks', () => {
    const [week] = groupWeeklyRows(rows)
    const ranked = rankWeeklyRows(week.rows, 'peak', 'asc')

    expect(ranked.map(({ row: value }) => value.ticker)).toEqual(['E', 'D', 'C', 'B', 'A'])
    expect(ranked.map(({ rank }) => rank)).toEqual([5, 4, 3, 2, 1])
  })
})
