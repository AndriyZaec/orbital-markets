import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Portfolio } from '../src/components/Portfolio'
import type { LivePosition } from '../src/hooks/useLivePositions'

const mocks = vi.hoisted(() => ({
  positions: [] as LivePosition[],
}))

vi.mock('@/hooks/useLivePositions', () => ({
  useLivePositions: () => ({ positions: mocks.positions, loading: false, error: null, refetch: vi.fn() }),
}))

vi.mock('@/hooks/useLiveActivity', () => ({
  useLiveActivity: () => ({
    items: [], nextCursor: null, loading: false, loadingMore: false, error: null,
    loadMore: vi.fn(), refetch: vi.fn(),
  }),
}))

vi.mock('@/hooks/useVenueReadiness', () => ({
  useVenueReadiness: () => {
    const venue = (name: string) => ({
      venue: name,
      label: name,
      status: 'ready',
      address: `${name}-account`,
      shortAddress: '0x1234',
      equity: 100,
      available: 80,
    })
    return {
      pacifica: venue('pacifica'),
      hyperliquid: venue('hyperliquid'),
      aster: venue('aster'),
      aggregate: { tradingReady: true },
    }
  },
}))

const position = (id: string, state: string, asset: string): LivePosition => ({
  id,
  plan_id: id,
  opportunity_id: id,
  asset,
  venue_a: 'aster',
  venue_b: 'pacifica',
  state,
  notional: 100,
  leverage: 1,
  entry_spread: 0,
  hedge_mismatch: 0,
  current_spread: 0.1,
  current_basis: 0,
  entry_basis: 0,
  basis_change: 0,
  price_pnl: 0,
  funding_pnl: 1,
  funding_pnl_source: 'estimated',
  total_pnl: 1,
  leg1_current_price: 0,
  leg2_current_price: 0,
  leg1_liq_price: 0,
  leg2_liq_price: 0,
  leg1_liq_dist: 0,
  leg2_liq_dist: 0,
  leg1_liq_risk: '',
  leg2_liq_risk: '',
  hold_hours: 1,
  started_at: '2026-09-30T12:00:00Z',
  updated_at: '2026-09-30T13:00:00Z',
})

afterEach(() => {
  cleanup()
  mocks.positions = []
})

describe('Portfolio position navigation', () => {
  it('shows at most five active positions including pending and selects the clicked position', async () => {
    const pending = position('pending-position', 'pending', 'PEND')
    mocks.positions = [
      position('closed-position', 'closed', 'CLOSED'),
      position('legacy-opening', 'opening', 'OPENING'),
      position('error-position', 'error', 'ERROR'),
      pending,
      position('open-1', 'open', 'BTC'),
      position('open-2', 'open', 'ETH'),
      position('open-3', 'degraded', 'SOL'),
      position('open-4', 'closing', 'DOGE'),
      position('open-5', 'open', 'SUI'),
    ]
    const onOpenPosition = vi.fn()
    const onViewPositions = vi.fn()

    render(
      <Portfolio
        onConnectWallets={() => {}}
        onViewPositions={onViewPositions}
        onOpenPosition={onOpenPosition}
      />,
    )

    const preview = screen.getByRole('region', { name: 'Live Positions' })
    expect(within(preview).queryByText('CLOSED')).toBeNull()
    expect(within(preview).queryByText('OPENING')).toBeNull()
    expect(within(preview).queryByText('ERROR')).toBeNull()
    expect(within(preview).getAllByRole('button')).toHaveLength(6)

    await userEvent.click(within(preview).getByRole('button', { name: /PEND/ }))
    expect(onOpenPosition).toHaveBeenCalledWith(pending)

    await userEvent.click(within(preview).getByRole('button', { name: 'View all open positions' }))
    expect(onViewPositions).toHaveBeenCalledOnce()
    expect(onOpenPosition).toHaveBeenCalledOnce()
  })
})
