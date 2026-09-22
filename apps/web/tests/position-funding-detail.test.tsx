import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PositionFundingDetail } from '../src/components/PositionFundingDetail'
import { LivePositionPanel } from '../src/components/LivePositionDetail'
import { LivePositions } from '../src/components/LivePositions'
import type { LivePositionChartContext } from '../src/lib/position-chart-context'
import type { LivePosition } from '../src/hooks/useLivePositions'
import type { LiveEventDetail } from '../src/hooks/useLivePositionDetail'

const mocks = vi.hoisted(() => ({
  chartContext: null as LivePositionChartContext | null,
  useHistory: vi.fn(),
  refetch: vi.fn(),
  positionsRefetch: vi.fn(),
  closePosition: vi.fn(),
  events: [] as LiveEventDetail[],
}))

vi.mock('@/hooks/useLivePositions', () => ({
  useLivePositions: () => ({ positions: [position, closedPosition], loading: false, error: null, refetch: mocks.positionsRefetch }),
}))

vi.mock('@/hooks/useVenueReadiness', () => ({
  useVenueReadiness: () => ({ aggregate: { tradingReady: true } }),
}))

vi.mock('@/hooks/useKillSwitch', () => ({
  useKillSwitch: () => ({
    state: {
      phase: 'idle', targeted: 0, totalRequests: 0, submitted: 0, succeeded: 0,
      failed: 0, uncertain: 0, signed: 0, positions: [], errors: [],
    },
    execute: vi.fn(),
    reset: vi.fn(),
  }),
}))

vi.mock('@/hooks/useLiveClose', () => ({
  useLiveClose: () => ({
    state: {
      phase: 'idle', submitted: 0, total: 0, failed: 0, succeeded: 0,
      reconciled: false, outcomes: [], errors: [],
    },
    closePosition: mocks.closePosition,
  }),
}))

vi.mock('@/hooks/useLivePositionDetail', () => ({
  useLivePositionDetail: () => ({
    data: mocks.chartContext ? { position, fills: [], events: mocks.events, chart_context: mocks.chartContext } : null,
    loading: false,
    error: null,
    refetch: mocks.refetch,
  }),
}))

vi.mock('@/hooks/useHistory', () => ({
  useHistory: mocks.useHistory,
}))

const position: LivePosition = {
  id: 'plan-fbdae6a2-6368-481f-bd9a-6180e0feff0e',
  plan_id: 'plan-fbdae6a2-6368-481f-bd9a-6180e0feff0e',
  opportunity_id: '2Z-pacifica-aster-long_b_short_a',
  asset: '2Z',
  venue_a: 'aster',
  venue_b: 'pacifica',
  state: 'open',
  notional: 15,
  leverage: 1,
  entry_spread: 0,
  hedge_mismatch: 0,
  current_spread: 0,
  current_basis: 0,
  entry_basis: 0,
  basis_change: 0,
  price_pnl: 0,
  funding_pnl: 0,
  funding_pnl_source: 'estimated',
  total_pnl: 0,
  leg1_current_price: 0,
  leg2_current_price: 0,
  leg1_liq_price: 0,
  leg2_liq_price: 0,
  leg1_liq_dist: 0,
  leg2_liq_dist: 0,
  leg1_liq_risk: '',
  leg2_liq_risk: '',
  hold_hours: 0,
  started_at: '2026-09-22T12:00:00Z',
  updated_at: '2026-09-22T12:00:00Z',
}

const closedPosition: LivePosition = {
  ...position,
  id: 'pos-closed',
  asset: 'SOL',
  state: 'closed',
  completed_at: '2026-09-22T13:00:00Z',
}

const availableContext: LivePositionChartContext = {
  available: true,
  asset: '2Z',
  venue_a: 'aster',
  venue_b: 'pacifica',
  direction: 'long_a_short_b',
  current_apr: 0,
  notional: 15,
  fee_estimate: 0.001,
  slippage_estimate: 0.0025,
  projection_source: 'execution_plan',
}

afterEach(() => {
  cleanup()
  mocks.chartContext = null
  mocks.useHistory.mockReset()
  mocks.refetch.mockReset()
  mocks.positionsRefetch.mockReset()
  mocks.closePosition.mockReset()
  mocks.events = []
})

describe('position-backed funding chart', () => {
  it('renders the persisted 2Z direction and history pair without a pre-trade return view', () => {
    mocks.chartContext = availableContext
    mocks.useHistory.mockReturnValue({
      data: [
        { t: '2026-09-22T10:00:00Z', funding_a: 0.0001, funding_b: 0.0003 },
        { t: '2026-09-22T11:00:00Z', funding_a: 0.0001, funding_b: 0.0003 },
      ],
      loading: false,
      error: null,
    })

    render(<PositionFundingDetail position={position} onBack={() => {}} />)

    expect(screen.getByText('Long Aster / Short Pacifica')).toBeTruthy()
    expect(screen.getByRole('tab', { name: 'Funding Rates' })).toBeTruthy()
    expect(screen.queryByRole('tab', { name: 'Potential Return' })).toBeNull()
    expect(mocks.useHistory).toHaveBeenCalledWith('2Z', 'aster', 'pacifica', '7d')
  })

  it('shows an explicit unavailable state without requesting history', () => {
    mocks.chartContext = {
      available: false,
      unavailable_reason: 'trustworthy long/short fill evidence is unavailable',
    }

    render(<PositionFundingDetail position={position} onBack={() => {}} />)

    expect(screen.getByText('trustworthy long/short fill evidence is unavailable')).toBeTruthy()
    expect(mocks.useHistory).not.toHaveBeenCalled()
    expect(screen.queryByRole('tab', { name: 'Funding Rates' })).toBeNull()
  })

  it('uses controlled row selection for the open-position sidebar', async () => {
    const onSelectPosition = vi.fn()
    const view = render(<LivePositions onSelectPosition={onSelectPosition} />)

    await userEvent.click(screen.getByText('2Z'))
    expect(onSelectPosition).toHaveBeenLastCalledWith(position)

    view.rerender(<LivePositions onSelectPosition={onSelectPosition} selectedPositionId={position.id} />)
    await userEvent.click(screen.getByText('2Z'))
    expect(onSelectPosition).toHaveBeenLastCalledWith(null)
  })

  it('keeps closed-position details in a modal', async () => {
    render(<LivePositions />)

    await userEvent.click(screen.getByRole('button', { name: /^Closed/ }))
    await userEvent.click(screen.getByText('SOL'))
    expect(screen.getByRole('button', { name: 'Close position panel' })).toBeTruthy()

    await userEvent.click(screen.getByRole('button', { name: 'Close position panel' }))
    expect(screen.queryByRole('button', { name: 'Close position panel' })).toBeNull()
  })

  it('keeps the close CTA below the scrollable sidebar details', () => {
    mocks.chartContext = availableContext
    mocks.events = [{
      id: 1,
      position_id: position.id,
      event: 'session_recovery_blocked',
      state: 'degraded',
      detail: 'A long operational event message that must wrap inside the narrow position sidebar.',
      at: '2026-09-22T12:01:00Z',
    }]
    render(<LivePositionPanel position={position} onClose={() => {}} />)

    const closeAction = screen.getByRole('button', { name: 'Close Position' })
    const positionInfo = screen.getByText('Position Info')
    expect(positionInfo.compareDocumentPosition(closeAction) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(closeAction.closest('[data-slot="position-close-cta"]')?.className).toContain('shrink-0')
    expect(screen.getByText(/long operational event message/).className).toContain('break-words')
  })

})
