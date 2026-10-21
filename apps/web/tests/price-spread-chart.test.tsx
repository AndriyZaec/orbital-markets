import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FundingChart } from '../src/components/FundingChart'
import { PriceSpreadChart } from '../src/components/PriceSpreadChart'
import type { HistoryPoint } from '../src/hooks/useHistory'

const mocks = vi.hoisted(() => ({ useHistory: vi.fn() }))
vi.mock('@/hooks/useHistory', () => ({ useHistory: mocks.useHistory }))

const data: HistoryPoint[] = [
  { t: '2026-10-20T10:00:00Z', basis: 0, price_spread: 0.003, edge: 0, funding_a: 0, funding_b: 0 },
  { t: '2026-10-20T10:01:00Z', basis: 0, price_spread: 0.001, edge: 0, funding_a: 0, funding_b: 0 },
]

afterEach(() => { cleanup(); mocks.useHistory.mockReset() })

describe('price spread history', () => {
  it('adds a tab with a separate 24h default and preserves the funding period', async () => {
    mocks.useHistory.mockReturnValue({ data, loading: false, error: null })
    render(<FundingChart asset="SOL" venueA="aster" venueB="pacifica" direction="long_a_short_b" currentApr={0} notional={100} feeEstimate={0} slippageEstimate={0} />)
    await userEvent.click(screen.getByRole('tab', { name: 'Price Spread' }))
    expect(mocks.useHistory).toHaveBeenLastCalledWith('SOL', 'aster', 'pacifica', '24h')
    expect(screen.getByRole('img', { name: /Price spread history/ })).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: '1H' }))
    expect(mocks.useHistory).toHaveBeenLastCalledWith('SOL', 'aster', 'pacifica', '1h')
    await userEvent.click(screen.getByRole('button', { name: '7D' }))
    expect(mocks.useHistory).toHaveBeenLastCalledWith('SOL', 'aster', 'pacifica', '7d')
    await userEvent.click(screen.getByRole('tab', { name: 'Funding Rates' }))
    expect(mocks.useHistory).toHaveBeenLastCalledWith('SOL', 'aster', 'pacifica', '7d')
  })

  it('uses the held direction, fill entry level and percentage-point change', () => {
    render(<PriceSpreadChart data={data} direction="long_b_short_a" range="1h" directionLabel="Long Pacifica / Short Aster" entry={{ value: -0.003, time: data[0].t }} />)
    expect(screen.getByText('-0.1000%')).toBeTruthy()
    expect(screen.getByText('-0.3000%')).toBeTruthy()
    expect(screen.getByText('+0.2000 pp')).toBeTruthy()
    expect(screen.getByLabelText('Position entry')).toBeTruthy()
    fireEvent.pointerMove(screen.getByRole('img'), { clientX: 100 })
    expect(screen.getByRole('img').innerHTML).not.toMatch(/NaN|Infinity/)
  })

  it('does not join missing prices or invent zero spread', () => {
    const { container } = render(<PriceSpreadChart data={[data[0], { ...data[0], t: '2026-10-20T10:00:30Z', price_spread: null }, data[1]]} direction="long_a_short_b" range="1h" directionLabel="Long Aster / Short Pacifica" />)
    const path = container.querySelector('path')?.getAttribute('d')
    expect(path?.match(/M /g)).toHaveLength(2)
    expect(path).not.toContain('L ')
  })

  it('handles a single zero sample and omits entry markers outside the period', () => {
    const { container } = render(<PriceSpreadChart data={[{ ...data[0], price_spread: 0 }]} direction="long_a_short_b" range="24h" directionLabel="Long Aster / Short Pacifica" entry={{ value: 0, time: '2026-10-19T00:00:00Z' }} />)
    expect(screen.queryByLabelText('Position entry')).toBeNull()
    expect(screen.getByLabelText('Entry spread level')).toBeTruthy()
    expect(container.innerHTML).not.toMatch(/NaN|Infinity/)
  })

  it('shows no price history when only funding samples exist', () => {
    render(<PriceSpreadChart data={[{ ...data[0], price_spread: null }]} direction="long_a_short_b" range="24h" directionLabel="Long Aster / Short Pacifica" />)
    expect(screen.getByText('No price spread history for this period.')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
  })
})
