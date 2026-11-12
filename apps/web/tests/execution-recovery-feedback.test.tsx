import { cleanup, fireEvent, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { renderWithQueryClient as render } from './render-with-query-client'
import { LiveExecutionModal } from '../src/components/LiveExecutionModal'
import type { LiveExecutionState, UnwindStatus } from '../src/hooks/useLiveExecution'

const diagnostic = 'Leg 2 signing failed: Invalid Phoenix order: instruction bytes manifest'
function state(unwindStatus: UnwindStatus, exposure = false): LiveExecutionState {
  return {
    phase: exposure ? 'degraded' : 'aborted', prepareStage: null, asset: 'BTC', sessionId: 'session', positionId: null,
    accountPacifica: null, accountHyperliquid: '0xowner', accounts: {}, riskierVenue: 'hyperliquid', hedgeVenue: 'phoenix',
    leg1Requests: [], leg2Request: null, leg1Fill: { filled_amount: 0.6, avg_price: 100, status: 'filled' },
    leg2Fill: null, mismatch: null, unwound: unwindStatus === 'confirmed', unwindStatus,
    error: diagnostic, reason: diagnostic, expiresAt: null, currentVenue: null, guardFailure: null,
    remainingExposure: exposure ? [{ leg: 1, venue: 'hyperliquid', symbol: 'BTC', side: 'long', amount: 0.6 }] : [],
  }
}
afterEach(cleanup)

it('shows confirmed closure rather than a never-opened failure for a failed response with a known fill', () => {
  render(<LiveExecutionModal state={{ ...state('confirmed'), phase: 'failed' }} onRetry={vi.fn()} onClose={vi.fn()} onViewPositions={vi.fn()} />)
  expect(screen.getByText('Open safely aborted')).toBeTruthy()
  expect(screen.queryByText(/No order was confirmed open/)).toBeNull()
  expect(screen.getByText('Closed')).toBeTruthy()
})

it('blocks retry when failed status conflicts with a recorded fill and closure is not confirmed', () => {
  render(<LiveExecutionModal state={{ ...state('unconfirmed'), phase: 'failed', notStarted: true }} onRetry={vi.fn()} onClose={vi.fn()} onViewPositions={vi.fn()} />)
  expect(screen.queryByRole('button', { name: 'Try Again' })).toBeNull()
  expect(screen.queryByText(/No order was confirmed open|No orders were submitted/)).toBeNull()
  expect(screen.getByRole('button', { name: 'Review Positions' })).toBeTruthy()
})

it('shows one confirmed recovery outcome without SDK diagnostics or a duplicate unwind explanation', () => {
  const retry = vi.fn()
  render(<LiveExecutionModal state={state('confirmed')} onRetry={retry} onClose={vi.fn()} onViewPositions={vi.fn()} />)
  expect(screen.getByText('Open safely aborted')).toBeTruthy()
  expect(screen.getByText('The filled leg was closed and no residual exposure was reported.')).toBeTruthy()
  expect(screen.queryByText(/instruction bytes manifest|Leg 2 signing failed|pre-signed order/)).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'Try Again' }))
  expect(retry).toHaveBeenCalledTimes(1)
})

it.each(['unconfirmed', 'submit_failed', 'requires_signature'] as const)('keeps %s exposure visible and routes to positions instead of retrying the open', (status) => {
  const retry = vi.fn(), positions = vi.fn()
  render(<LiveExecutionModal state={state(status, true)} onRetry={retry} onClose={vi.fn()} onViewPositions={positions} />)
  expect(screen.getByText('Residual exposure detected')).toBeTruthy()
  expect(screen.getByText(/hyperliquid · long 0.6000 BTC/)).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Try Again' })).toBeNull()
  expect(screen.queryByText(/instruction bytes manifest/)).toBeNull()
  if (status === 'requires_signature') expect(screen.getByText('Your signature is needed to close the remaining position.')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Review & Close Exposure' }))
  expect(positions).toHaveBeenCalledTimes(1)
  expect(retry).not.toHaveBeenCalled()
})

it('does not turn uncertain recovery with no exact residual amount into a safe abort', () => {
  render(<LiveExecutionModal state={state('unconfirmed')} onRetry={vi.fn()} onClose={vi.fn()} onViewPositions={vi.fn()} />)
  expect(screen.getByText('Open aborted with uncertain exposure')).toBeTruthy()
  expect(screen.queryByText('Open safely aborted')).toBeNull()
  expect(screen.getByRole('button', { name: 'Review Positions' })).toBeTruthy()
})
