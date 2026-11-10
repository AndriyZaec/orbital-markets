import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useLiveBalances } from '../src/hooks/useLiveBalances'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), subscribe: vi.fn(), unsubscribe: vi.fn() }))
vi.mock('@/lib/api', () => ({ apiFetch: mocks.apiFetch }))
vi.mock('@/lib/live-events', () => ({ subscribeLiveAccountUpdates: mocks.subscribe }))
vi.mock('@/hooks/usePageVisibility', () => ({ usePageVisibility: () => true }))
vi.mock('@/hooks/useLiveExecution', () => ({ useLiveExecution: () => ({ state: { phase: 'idle', accounts: {} } }) }))

beforeEach(() => {
  vi.clearAllMocks()
  mocks.apiFetch.mockImplementation(async () => Response.json({}))
  mocks.subscribe.mockReturnValue(mocks.unsubscribe)
})
afterEach(() => { cleanup(); vi.useRealTimers() })

it('expires execution-facing freshness when both transports stop, retaining the monetary observation', async () => {
  vi.useFakeTimers()
  const { result } = renderHook(() => useLiveBalances('OwnerA', null, null, 'OwnerA'))
  await act(() => vi.advanceTimersByTimeAsync(0))
  act(() => mocks.subscribe.mock.calls[0][1]({ type: 'balances', data: {
    phoenix: { venue: 'phoenix', connected: true, stream_ready: true, fresh: true, equity: 100, available: 80,
      age_seconds: 0, last_updated: new Date().toISOString() },
  } }))
  mocks.apiFetch.mockImplementation(async () => { throw new TypeError('offline') })
  act(() => mocks.subscribe.mock.calls[0][1]({ type: 'disconnected' }))
  await act(() => vi.advanceTimersByTimeAsync(30_001))
  expect(result.current.phoenix.fresh).toBe(false)
  expect(result.current.phoenix.observation?.equity).toBe(100)
})

it('preserves unchanged owners and their observations when another wallet connects or risk becomes unavailable', async () => {
  const { result, rerender } = renderHook(({ evm }) => useLiveBalances('OwnerA', evm, null, 'OwnerA'), { initialProps: { evm: null as string | null } })
  await waitFor(() => expect(mocks.subscribe).toHaveBeenCalledTimes(1))
  act(() => mocks.subscribe.mock.calls[0][1]({ type: 'balances', data: {
    phoenix: { venue: 'phoenix', connected: true, stream_ready: true, fresh: true, equity: 100, available: 80, last_updated: '2026-11-10T12:00:00Z' },
  } }))
  rerender({ evm: '0xHL' })
  expect(result.current.phoenix.equity).toBe(100)
  act(() => mocks.subscribe.mock.calls[1][1]({ type: 'balances', data: {
    phoenix: { venue: 'phoenix', connected: true, stream_ready: true, fresh: false, unavailable: true, equity: 0, available: 0 },
  } }))
  expect(result.current.phoenix.observation?.equity).toBe(100)
  expect(result.current.phoenix.fresh).toBe(false)
  expect(result.current.phoenix.unavailable).toBe(true)
})

it('uses one account subscription for all venues and preserves healthy balances beside an unavailable venue', async () => {
  const { result, unmount } = renderHook(() => useLiveBalances('SolanaOwner', '0xHL', '0xAster', 'SolanaOwner'))
  await waitFor(() => expect(mocks.apiFetch).toHaveBeenCalledTimes(1))
  expect(mocks.subscribe).toHaveBeenCalledTimes(1)
  expect(mocks.subscribe.mock.calls[0][0]).toEqual({
    pacifica: 'SolanaOwner', hyperliquid: '0xhl', aster: '0xaster', phoenix: 'SolanaOwner',
  })
  act(() => mocks.subscribe.mock.calls[0][1]({ type: 'balances', data: {
    pacifica: { venue: 'pacifica', connected: true, fresh: true, equity: 100, available: 80 },
    hyperliquid: { venue: 'hyperliquid', connected: true, fresh: true, equity: 200, available: 180 },
    aster: { venue: 'aster', connected: true, fresh: true, equity: 300, available: 280 },
    phoenix: { venue: 'phoenix', connected: false, fresh: false, unavailable: true, reason: 'Phoenix unavailable' },
  } }))
  expect(result.current.pacifica.equity).toBe(100)
  expect(result.current.hyperliquid.equity).toBe(200)
  expect(result.current.aster.equity).toBe(300)
  expect(result.current.phoenix.unavailable).toBe(true)
  act(() => mocks.subscribe.mock.calls[0][1]({ type: 'balances', data: {
    phoenix: { venue: 'phoenix', connected: true, fresh: true, equity: 400, available: 380 },
  } }))
  expect(result.current.phoenix.equity).toBe(400)
  expect(result.current.pacifica.equity).toBe(100)
  expect(mocks.subscribe).toHaveBeenCalledTimes(1)
  unmount()
  expect(mocks.unsubscribe).toHaveBeenCalledTimes(1)
})

it('polls only when the shared SSE is disconnected and replaces the subscription on owner change', async () => {
  vi.useFakeTimers()
  const { rerender, unmount } = renderHook(({ owner }) => useLiveBalances(owner, '0xHL', null, owner), {
    initialProps: { owner: 'OwnerA' },
  })
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(mocks.apiFetch).toHaveBeenCalledTimes(1)
  act(() => mocks.subscribe.mock.calls[0][1]({ type: 'connected' }))
  await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
  expect(mocks.apiFetch).toHaveBeenCalledTimes(1)
  act(() => mocks.subscribe.mock.calls[0][1]({ type: 'disconnected' }))
  await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
  expect(mocks.apiFetch).toHaveBeenCalledTimes(2)
  rerender({ owner: 'OwnerB' })
  expect(mocks.unsubscribe).toHaveBeenCalledTimes(1)
  expect(mocks.subscribe).toHaveBeenCalledTimes(2)
  expect(mocks.subscribe.mock.calls[1][0]).toEqual({ pacifica: 'OwnerB', hyperliquid: '0xhl', phoenix: 'OwnerB' })
  unmount()
  expect(mocks.unsubscribe).toHaveBeenCalledTimes(2)
})
