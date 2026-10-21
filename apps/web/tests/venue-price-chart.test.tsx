import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { VenuePriceChart } from '../src/components/VenuePriceChart'
import type { HistoryPoint } from '../src/hooks/useHistory'

const start = Date.parse('2026-10-21T10:00:00Z')
function sample(seconds: number, long: number | null, short: number | null): HistoryPoint {
  return { t: new Date(start + seconds * 1000).toISOString(), basis: 0, edge: 0, funding_a: 0, funding_b: 0,
    mark_price_a: long, mark_price_b: short, price_spread: 0.002 }
}
const props = { direction: 'long_a_short_b' as const, venueA: 'hyperliquid', venueB: 'aster' }
function movePointer(x: number) {
  const svg = screen.getByRole('img')
  svg.getBoundingClientRect = () => ({ x: 0, y: 0, left: 0, top: 0, right: 1100, bottom: 210, width: 1100, height: 210, toJSON: () => ({}) })
  fireEvent.pointerMove(svg, { clientX: x })
}
afterEach(cleanup)

describe('venue-labelled opportunity prices', () => {
  it('labels both curves by venue and side using the existing venue colors', () => {
    const { container } = render(<VenuePriceChart {...props} data={[sample(0, 100, 100.2), sample(180, 101, 101.2)]} />)
    expect(screen.getByLabelText('Hyperliquid Long price curve').getAttribute('stroke')).toBe('#a78bfa')
    expect(screen.getByLabelText('Aster Short price curve').getAttribute('stroke')).toBe('#f59e0b')
    expect(screen.getByText('Hyperliquid · Long')).toBeTruthy()
    expect(screen.getByText('Aster · Short')).toBeTruthy()
    expect(screen.getByLabelText('Hyperliquid Long price curve').getAttribute('d')).toContain(' C ')
    expect(container.querySelectorAll('path')).toHaveLength(2)
    expect(container.querySelector('path:not([fill="none"])')).toBeNull()
  })

  it('snaps hover to rendered knots instead of an outlier removed by compression', () => {
    render(<VenuePriceChart {...props} data={[
      sample(0, 100, 100.2), sample(20, 500, 501), sample(40, 101, 101.2), sample(60, 102, 102.2), sample(80, 103, 103.2),
    ]} />)
    movePointer(344) // Raw t=20s was removed; nearest rendered knot is t=0.
    expect(screen.getByLabelText('Price observation').getAttribute('data-time')).toBe(String(start))
    expect(screen.getByText(/Hyperliquid \$100.00 · Aster \$100.20 · Basis/)).toBeTruthy()
    for (const [markerLabel, curveLabel] of [
      ['Long price marker', 'Hyperliquid Long price curve'], ['Short price marker', 'Aster Short price curve'],
    ]) {
      const marker = screen.getByLabelText(markerLabel)
      const coordinates = `${marker.getAttribute('cx')} ${marker.getAttribute('cy')}`
      expect(screen.getByLabelText(curveLabel).getAttribute('d')).toContain(coordinates)
    }
    expect(screen.getByRole('img').innerHTML).not.toMatch(/NaN|Infinity/)
  })

  it('keeps missing prices as gaps and hides hover in the gap and outside the plot', () => {
    render(<VenuePriceChart {...props} data={[sample(0, 100, 100.2), sample(60, null, 100.5), sample(120, 101, 101.2)]} />)
    expect(screen.getByLabelText('Hyperliquid Long price curve').getAttribute('d')?.match(/M /g)).toHaveLength(2)
    movePointer(588)
    expect(screen.queryByLabelText('Price observation')).toBeNull()
    movePointer(50)
    expect(screen.queryByLabelText('Price observation')).toBeNull()
  })

  it('keeps venue colors when long/short reverse and puts the legend below the chart', () => {
    render(<VenuePriceChart {...props} direction="long_b_short_a" data={[sample(0, 100, 100)]} />)
    expect(screen.getByLabelText('Aster Long price curve').getAttribute('stroke')).toBe('#f59e0b')
    expect(screen.getByLabelText('Hyperliquid Short price curve').getAttribute('stroke')).toBe('#a78bfa')
    expect(screen.getByText('Aster · Long').closest('svg')).toBeNull()
    expect(screen.getByText('Hyperliquid · Short').closest('svg')).toBeNull()
    expect(screen.getByRole('img').compareDocumentPosition(screen.getByText('Aster · Long')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByText('Mark Prices')).toBeTruthy()
  })

  it('uses the last duplicate timestamp and handles unavailable pairs without fabricating prices', () => {
    const view = render(<VenuePriceChart {...props} data={[sample(0, 100, 100.2), sample(0, 102, 102.2)]} />)
    expect(screen.getByText('Long · Hyperliquid').parentElement?.textContent).toBe('Long · Hyperliquid$102.00')
    expect(screen.getByRole('img').innerHTML).not.toMatch(/NaN|Infinity/)
    view.rerender(<VenuePriceChart {...props} data={[sample(0, null, 102.2)]} />)
    expect(screen.getByText('No paired price history for this pair.')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })
})
