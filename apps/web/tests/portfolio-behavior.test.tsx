import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Portfolio } from '../src/components/Portfolio'
import type { LivePosition } from '../src/hooks/useLivePositions'
import type { LiveActivityItem } from '../src/hooks/useLiveActivity'

const mocks = vi.hoisted(() => ({
  positions: [] as LivePosition[],
  activityItems: [] as LiveActivityItem[],
}))

vi.mock('@/hooks/useLivePositions', () => ({
  useLivePositions: () => ({ positions: mocks.positions, loading: false, error: null, refetch: vi.fn() }),
}))

vi.mock('@/hooks/useLiveActivity', () => ({
  useLiveActivity: () => ({
    items: mocks.activityItems, nextCursor: null, loading: false, loadingMore: false, error: null,
    loadMore: vi.fn(), refetch: vi.fn(),
  }),
}))

vi.mock('@/components/PositionShareDialog', () => ({
  PositionShareDialog: ({ kind, onOpenChange }: { kind: string; onOpenChange: (open: boolean) => void }) => (
    <div role="dialog" aria-label={`Share ${kind} position`}>
      <span>{kind === 'active' ? 'Active Position' : 'Closed Position'}</span>
      <button type="button" onClick={() => onOpenChange(false)}>Close share</button>
    </div>
  ),
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
  mocks.activityItems = []
})

describe('Portfolio activity sharing', () => {
  it('shares active and closed positions without changing the action-column geometry', async () => {
    const active = position('active-position', 'open', 'SOL')
    const closed = { ...position('closed-position', 'closed', 'BTC'), completed_at: '2026-09-30T13:00:00Z' }
    mocks.activityItems = [
      { id: 'opened-active', type: 'opened', at: active.started_at, position: active },
      { id: 'closed-complete', type: 'closed', at: closed.completed_at, position: closed },
    ]
    const onOpenPosition = vi.fn()

    render(
      <Portfolio
        onConnectWallets={() => {}}
        onViewPositions={() => {}}
        onOpenPosition={onOpenPosition}
      />,
    )

    const activeShare = screen.getByRole('button', { name: 'Share SOL active position' })
    const closedShare = screen.getByRole('button', { name: 'Share BTC closed position' })
    expect(activeShare.className).toContain('w-20')
    expect(closedShare.className).toContain('w-20')

    await userEvent.click(activeShare)
    expect(screen.getByRole('dialog', { name: 'Share active position' })).toBeTruthy()
    expect(screen.getByText('Active Position')).toBeTruthy()
    expect(onOpenPosition).not.toHaveBeenCalled()

    await userEvent.click(screen.getByRole('button', { name: 'Close share' }))
    await waitFor(() => expect(document.activeElement).toBe(activeShare))

    await userEvent.click(closedShare)
    expect(screen.getByRole('dialog', { name: 'Share closed position' })).toBeTruthy()
    expect(screen.getByText('Closed Position')).toBeTruthy()
  })
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
