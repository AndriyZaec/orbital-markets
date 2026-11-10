import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { AccountBalance } from '../src/components/AccountBalance'
import type { VenueId, VenueReadiness } from '../src/hooks/useVenueReadiness'

function readiness(venue: VenueId, changes: Partial<VenueReadiness> = {}): VenueReadiness {
  return {
    venue, label: venue, address: 'OwnerA', shortAddress: 'OwnerA', walletConnected: true,
    signerReady: true, agentReady: true, agentAddress: 'agent', agentStatus: 'ready',
    balanceConnected: true, balanceReady: true, streamReady: true, accountFresh: true,
    ageSeconds: 0, lastUpdated: '2026-11-10T12:00:00Z', equity: 100, available: 80,
    balanceObservation: { equity: changes.equity ?? 100, available: changes.available ?? 80, observed_at: '2026-11-10T12:00:00Z' },
    status: 'ready', blockingReasons: [], ...changes,
  }
}

afterEach(() => { cleanup(); vi.useRealTimers() })

it.each(['phoenix', 'pacifica', 'hyperliquid', 'aster'] as const)('quietly refreshes %s balances without treating cached amounts as ready data', async (venue) => {
  vi.useFakeTimers()
  const view = render(<AccountBalance readiness={readiness(venue)} />)
  expect(screen.getByText('$100.00 / $80.00')).toBeTruthy()
  const pending = readiness(venue, { balanceReady: false, accountFresh: false, equity: null, available: null,
    status: 'unavailable', blockingReasons: ['internal risk revision diagnostic'] })
  view.rerender(<AccountBalance readiness={pending} />)
  expect(screen.getByText('$100.00 / $80.00').className).toContain('text-muted-foreground')
  expect(screen.getByText('Last update')).toBeTruthy()
  expect(screen.queryByText('Ready')).toBeNull()
  expect(screen.queryByText(/risk revision/)).toBeNull()
  expect(screen.queryByRole('status')).toBeNull()
  await act(() => vi.advanceTimersByTimeAsync(300))
  expect(screen.queryByRole('status')).toBeNull()
  expect(screen.queryByText(/Last known|Unavailable|Ready/)).toBeNull()
  expect(pending.balanceReady).toBe(false)
  expect(pending.available).toBeNull()
  view.rerender(<AccountBalance readiness={readiness(venue, { equity: 120, available: 90 })} />)
  expect(screen.getByText('$120.00 / $90.00')).toBeTruthy()
  expect(screen.queryByText('Ready')).toBeNull()
  expect(screen.queryByRole('status')).toBeNull()
})

it('keeps a shared observation through long refreshes without restarting loading and isolates another owner', async () => {
  vi.useFakeTimers()
  const view = render(<AccountBalance readiness={readiness('phoenix')} />)
  const pending = { balanceReady: false, equity: null, available: null, status: 'unavailable' as const }
  view.rerender(<AccountBalance readiness={readiness('phoenix', pending)} />)
  await act(() => vi.advanceTimersByTimeAsync(20_000))
  expect(screen.queryByText('Unavailable')).toBeNull()
  expect(screen.queryByRole('status')).toBeNull()
  expect(screen.getByText('$100.00 / $80.00')).toBeTruthy()
  view.rerender(<AccountBalance readiness={readiness('phoenix')} />)
  view.rerender(<AccountBalance readiness={readiness('phoenix', pending)} />)
  expect(screen.queryByText('Unavailable')).toBeNull()
  await act(() => vi.advanceTimersByTimeAsync(300))
  expect(screen.queryByRole('status')).toBeNull()
  view.rerender(<AccountBalance readiness={readiness('phoenix', { ...pending, address: 'OwnerB', balanceObservation: undefined })} />)
  expect(screen.queryByText('$100.00 / $80.00')).toBeNull()
  expect(screen.queryByRole('status')).toBeNull()
})

it('does not flash a spinner for a fast first account snapshot', async () => {
  vi.useFakeTimers()
  const view = render(<AccountBalance readiness={readiness('phoenix', { balanceReady: false, equity: null, available: null, balanceObservation: undefined })} />)
  await act(() => vi.advanceTimersByTimeAsync(100))
  view.rerender(<AccountBalance readiness={readiness('phoenix')} />)
  await act(() => vi.advanceTimersByTimeAsync(500))
  expect(screen.queryByRole('status')).toBeNull()
  expect(screen.queryByText('Ready')).toBeNull()
})
