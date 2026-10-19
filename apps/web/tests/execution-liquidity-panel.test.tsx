import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { OpportunityPanel } from '../src/components/OpportunityPanel'
import type { Opportunity } from '../src/hooks/useOpportunities'
import type { ExecutionPlan } from '../src/hooks/usePlan'

const mocks = vi.hoisted(() => ({ execute: vi.fn(), refresh: vi.fn().mockResolvedValue(undefined), executable: true, impact: 0.003, planReady: true }))
vi.mock('@/hooks/useLiveExecution', () => ({ useLiveExecution: () => ({ state: { phase: 'idle' }, executeLive: mocks.execute, reset: vi.fn() }) }))
vi.mock('@/components/LiveExecutionModal', () => ({ LiveExecutionModal: () => null }))
vi.mock('@/lib/analytics', () => ({ trackAnalytics: vi.fn() }))
vi.mock('@/hooks/useVenueReadiness', () => ({ useVenueReadiness: () => ({
  pacifica: { venue: 'pacifica', address: 'pac-owner', status: 'ready', available: 100000 },
  hyperliquid: { venue: 'hyperliquid', address: 'hl-owner', status: 'ready', available: 100000 },
  aster: { venue: 'aster', address: null, status: 'disconnected', available: null },
  refreshBalances: mocks.refresh,
}) }))
vi.mock('@/hooks/usePlan', () => ({ usePlan: (_id: string, leverage: number, notional: number) => ({
  plan: mocks.planReady ? makePlan(leverage, notional) : null, loading: !mocks.planReady, error: null, maxLeverage: null, leverageCapabilityBlocked: false, refresh: mocks.refresh,
}) }))

function makePlan(leverage = 2, notional = 500): ExecutionPlan {
  const leg = { asset: 'BTC', expected_price: 100, slippage: 0.0015, fee: 0.0005, leverage,
    margin_required: notional / leverage, liquidation_price: 50, liquidation_distance: 0.5 }
  return { id: 'plan', opportunity_id: 'opp', asset: 'BTC', direction: 'long_a_short_b', notional,
    base_amount: notional / 100, best_price_capacity: 100, max_leverage: 2,
    leg_1: { ...leg, venue: 'pacifica', side: 'long', limit_price: 102 },
    leg_2: { ...leg, venue: 'hyperliquid', side: 'short', limit_price: 98 },
    leverage: { leverage, margin_required: notional, gross_exposure: 2 * notional, effective_leverage: leverage },
    expected_spread: 0, estimated_net_edge: 0.1, bounds: { max_slippage_pct: 0.005, max_entry_spread_pct: 0.01, min_net_edge_pct: 0.01 },
    liquidity: { status: mocks.executable ? 'l2' : 'insufficient', impact: mocks.impact, requires_acknowledgement: mocks.impact > 0.002, observed_at: new Date().toISOString() },
    risk_tier: 'standard', confidence: 'high', executable: mocks.executable,
    warnings: mocks.executable ? [] : ['Insufficient visible order-book depth'], created_at: '', expires_at: '',
  }
}

const opportunity: Opportunity = { id: 'opp', asset: 'BTC', detected_at: '', venue_pair: { venue_a: 'pacifica', venue_b: 'hyperliquid' },
  direction: 'long_a_short_b', funding_rate_a: 0.001, funding_rate_b: 0.002, funding_spread: -0.001,
  annualized_gross_edge: 0.1, entry_spread_estimate: 0, slippage_estimate: 0, fee_estimate: 0.001, estimated_net_edge: 0.099,
  available_notional: 100000, best_price_capacity: 100, recommended_notional: 100, max_leverage: 2,
  liquidity: 'thin', liq_suspect: false, confidence: 'high', risk_tier: 'standard', status: 'available', generation: 1,
  source_revisions: {}, availability_reasons: null, execution_status: 'executable', risk_flags: null, warnings: null,
  signal_7d: null, signal_7d_state: 'unavailable', signal_7d_version: 0,
}
const props = { opportunity, lastUpdated: null, mode: 'live' as const, notionalInput: '500', onNotionalInputChange: vi.fn(), onClose: vi.fn(), onExecute: vi.fn() }
afterEach(() => { cleanup(); mocks.execute.mockClear(); mocks.executable = true; mocks.impact = 0.003; mocks.planReady = true })

describe('automatic execution depth and acknowledgement', () => {
  it('keeps the margin summary slot mounted while the opening plan loads', () => {
    mocks.planReady = false
    const view = render(<OpportunityPanel {...props} />)
    const summary = screen.getByText('Margin --').parentElement
    expect(summary?.getAttribute('aria-hidden')).toBe('true')
    mocks.planReady = true
    view.rerender(<OpportunityPanel {...props} />)
    expect(screen.getByText('Margin $500.00').parentElement).toBe(summary)
    expect(screen.getByText('Exposure $1.0K').parentElement).toBe(summary)
    expect(summary?.getAttribute('aria-hidden')).toBe('false')
  })

  it('uses one acknowledgement above Execute and carries the approval into execution', async () => {
    render(<OpportunityPanel {...props} />)
    const button = screen.getByRole('button', { name: 'Execute Live' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    expect(screen.getAllByRole('checkbox')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: /refresh.*l2/i })).toBeNull()
    expect(screen.queryByText(/Estimated L2 capacity/)).toBeNull()
    expect(screen.getByRole('button', { name: /Suggested:/ })).toBeTruthy()
    fireEvent.click(screen.getByRole('checkbox', { name: /Acknowledge elevated price impact/ }))
    expect(button.disabled).toBe(false)
    fireEvent.click(button)
    await waitFor(() => expect(mocks.execute).toHaveBeenCalledTimes(1))
    expect(mocks.execute.mock.calls[0][0]).toMatchObject({ impactAcknowledged: true, requestedNotional: 500, approvedBaseAmount: 5 })
  })

  it('does not restore an old acknowledgement when a size or direction is changed and changed back', () => {
    const view = render(<OpportunityPanel {...props} />)
    fireEvent.click(screen.getByRole('checkbox'))
    expect(screen.getByRole('checkbox').getAttribute('aria-checked')).toBe('true')
    view.rerender(<OpportunityPanel {...props} notionalInput="600" />)
    expect(screen.getByRole('checkbox').getAttribute('aria-checked')).toBe('false')
    view.rerender(<OpportunityPanel {...props} />)
    expect(screen.getByRole('checkbox').getAttribute('aria-checked')).toBe('false')
    fireEvent.click(screen.getByRole('checkbox'))
    view.rerender(<OpportunityPanel {...props} opportunity={{ ...opportunity, direction: 'long_b_short_a' }} />)
    expect(screen.getByRole('checkbox').getAttribute('aria-checked')).toBe('false')
  })

  it('acknowledgement cannot override missing full-position depth', () => {
    mocks.executable = false
    render(<OpportunityPanel {...props} />)
    fireEvent.click(screen.getByRole('checkbox'))
    expect((screen.getByRole('button', { name: 'Execute Live' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText('Insufficient visible order-book depth')).toBeTruthy()
  })

  it('needs no additional confirmation at or below the impact benchmark', () => {
    mocks.impact = 0.002
    render(<OpportunityPanel {...props} />)
    expect(screen.queryByRole('checkbox')).toBeNull()
    expect((screen.getByRole('button', { name: 'Execute Live' }) as HTMLButtonElement).disabled).toBe(false)
  })
})
