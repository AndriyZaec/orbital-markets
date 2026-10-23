import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FundingChart } from '../src/components/FundingChart'
import { PriceSpreadChart } from '../src/components/PriceSpreadChart'
import type { HistoryPoint } from '../src/hooks/useHistory'

const mocks = vi.hoisted(() => ({ useHistory: vi.fn() }))
vi.mock('@/hooks/useHistory', () => ({ useHistory: mocks.useHistory }))

const data: HistoryPoint[] = [
  { t: '2026-10-20T10:00:00Z', basis: 0, price_spread: 0.003, mark_price_a: 100, mark_price_b: 100.3, edge: 0, funding_a: 0, funding_b: 0 },
  { t: '2026-10-20T10:01:00Z', basis: 0, price_spread: 0.001, mark_price_a: 100, mark_price_b: 100.1, edge: 0, funding_a: 0, funding_b: 0 },
]

afterEach(() => { cleanup(); mocks.useHistory.mockReset() })

describe('price spread history', () => {
  it('fixes price history to 24h without a period selector and preserves the funding period', async () => {
    mocks.useHistory.mockReturnValue({ data, loading: false, error: null })
    render(<FundingChart asset="SOL" venueA="aster" venueB="pacifica" direction="long_a_short_b" currentApr={0} notional={100} feeEstimate={0} slippageEstimate={0} />)
    await userEvent.click(screen.getByRole('tab', { name: 'Price Basis' }))
    expect(mocks.useHistory).toHaveBeenLastCalledWith('SOL', 'aster', 'pacifica', '24h', 'price_spread')
    expect(screen.getByRole('img', { name: 'Venue price history' })).toBeTruthy()
    expect(screen.getByText('Last 24H')).toBeTruthy()
    expect(screen.getByText('+0.1000%')).toBeTruthy()
    expect(screen.getByText('Long · Aster')).toBeTruthy()
    expect(screen.getByText('Short · Pacifica')).toBeTruthy()
    expect(screen.getByText('$100.00')).toBeTruthy()
    expect(screen.getByText('$100.10')).toBeTruthy()
    expect(screen.queryByRole('group', { name: 'Price spread period' })).toBeNull()
    expect(screen.queryByRole('button', { name: /^(1H|24H|7D|D|W|M)$/ })).toBeNull()
    await userEvent.click(screen.getByRole('tab', { name: 'Funding Rates' }))
    expect(mocks.useHistory).toHaveBeenLastCalledWith('SOL', 'aster', 'pacifica', '7d', 'funding')
  })

  it('uses the held direction, fill entry level and percentage-point change', () => {
    render(<PriceSpreadChart data={data} direction="long_b_short_a" directionLabel="Long Pacifica / Short Aster" entry={{ value: -0.003, time: data[0].t }} />)
    expect(screen.getByText('-0.1000%')).toBeTruthy()
    expect(screen.getByText('-0.3000%')).toBeTruthy()
    expect(screen.getByText('+0.2000 pp')).toBeTruthy()
    expect(screen.getByLabelText('Position entry')).toBeTruthy()
    fireEvent.pointerMove(screen.getByRole('img'), { clientX: 100 })
    expect(screen.getByRole('img').innerHTML).not.toMatch(/NaN|Infinity/)
  })

  it('does not join missing prices or invent zero spread', () => {
    const { container } = render(<PriceSpreadChart data={[data[0], { ...data[0], t: '2026-10-20T10:00:30Z', price_spread: null }, data[1]]} direction="long_a_short_b" directionLabel="Long Aster / Short Pacifica" />)
    const path = container.querySelector('path[fill="none"]')?.getAttribute('d')
    expect(path?.match(/M /g)).toHaveLength(2)
    expect(path).not.toContain('L ')
  })

  it('handles a single zero sample and omits entry markers outside the period', () => {
    const { container } = render(<PriceSpreadChart data={[{ ...data[0], price_spread: 0 }]} direction="long_a_short_b" directionLabel="Long Aster / Short Pacifica" entry={{ value: 0, time: '2026-10-19T00:00:00Z' }} />)
    expect(screen.queryByLabelText('Position entry')).toBeNull()
    expect(screen.getByLabelText('Entry spread level')).toBeTruthy()
    expect(container.innerHTML).not.toMatch(/NaN|Infinity/)
  })

  it('shows no price history when only funding samples exist', () => {
    render(<PriceSpreadChart data={[{ ...data[0], price_spread: null }]} direction="long_a_short_b" directionLabel="Long Aster / Short Pacifica" />)
    expect(screen.getByText('No price spread history for this period.')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })

  it('keeps a recent entry visible without claiming a change from pre-entry observations', () => {
    const view = render(<PriceSpreadChart data={data} direction="long_a_short_b" directionLabel="Long Aster / Short Pacifica"
      entry={{ value: 0.002, time: '2026-10-20T10:03:00Z' }}
      window={{ start: '2026-10-19T10:04:00Z', end: '2026-10-20T10:04:00Z' }} />)
    expect(screen.getByLabelText('Position entry')).toBeTruthy()
    expect(screen.getByText('Change since entry').parentElement?.textContent).toBe('Change since entry—')
    view.rerender(<PriceSpreadChart data={[...data, { ...data[0], t: '2026-10-20T10:04:00Z', price_spread: 0.001 }]}
      direction="long_a_short_b" directionLabel="Long Aster / Short Pacifica"
      entry={{ value: 0.002, time: '2026-10-20T10:03:00Z' }}
      window={{ start: '2026-10-19T10:04:00Z', end: '2026-10-20T10:04:00Z' }} />)
    expect(screen.getByText('-0.1000 pp')).toBeTruthy()
  })

  it('does not claim a since-entry change when the opening time is unknown', () => {
    render(<PriceSpreadChart data={data} direction="long_a_short_b" directionLabel="Long Aster / Short Pacifica" entry={{ value: 0.002 }} />)
    expect(screen.getByText('Change since entry').parentElement?.textContent).toBe('Change since entry—')
  })

  it('fits short history across the plot, keeps zero centered and smooths without showing every dot', () => {
    const { container } = render(<PriceSpreadChart data={data} direction="long_a_short_b" directionLabel="Long Aster / Short Pacifica"
      window={{ start: '2026-10-19T10:04:00Z', end: '2026-10-20T10:04:00Z' }} />)
    const path = screen.getByLabelText('Premium trend').getAttribute('d')
    expect(path).toContain('M 78 ')
    expect(path).toContain(' C ')
    expect(path).toContain('1076 ')
    expect(container.querySelector('line[stroke-dasharray="3 4"]')?.getAttribute('y1')).toBe('95')
    expect(container.querySelectorAll('circle')).toHaveLength(1)
  })

  it('maps prices to long then short when the direction is reversed', () => {
    render(<PriceSpreadChart data={data} direction="long_b_short_a" directionLabel="Long Pacifica / Short Aster" longLabel="Pacifica" shortLabel="Aster" />)
    expect(screen.getByText('Short is 0.1000% cheaper')).toBeTruthy()
    expect(screen.getByText('Long · Pacifica').parentElement?.textContent).toBe('Long · Pacifica$100.10')
    expect(screen.getByText('Short · Aster').parentElement?.textContent).toBe('Short · Aster$100.00')
  })
})
