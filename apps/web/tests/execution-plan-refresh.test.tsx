import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'
import { usePlan } from '../src/hooks/usePlan'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), visible: true }))
vi.mock('@/lib/api', async (importOriginal) => ({ ...await importOriginal<typeof import('../src/lib/api')>(), apiFetch: mocks.fetch }))
vi.mock('@/hooks/usePageVisibility', () => ({ usePageVisibility: () => mocks.visible }))
afterEach(() => { cleanup(); vi.useRealTimers(); mocks.fetch.mockReset(); mocks.visible = true })
const response = (executable: boolean, notional = 500, direction = 'long_a_short_b') => new Response(JSON.stringify({
  opportunity_id: 'opp', direction, notional, max_leverage: 2, executable,
  liquidity: { status: executable ? 'l2' : 'unavailable', requires_acknowledgement: executable, impact: executable ? 0.003 : 0 },
}))

describe('automatic plan depth refresh', () => {
  it.each(['unavailable', 'degraded'])('treats a temporary %s opportunity as pending data and restores the same requested plan automatically', async (status) => {
    vi.useFakeTimers()
    mocks.fetch.mockResolvedValueOnce(response(true))
      .mockResolvedValueOnce(Response.json({
        error: 'opportunity XPL-hyperliquid-phoenix is unavailable',
        opportunity_status: status,
        availability_reasons: [{ code: 'source_fetch_failed', venue: 'phoenix' }],
      }, { status: 422 }))
      .mockResolvedValueOnce(response(true))
    const accounts = { hyperliquid: '0xowner', phoenix: 'SolanaOwner' }
    const view = renderHook(() => usePlan('opp', 2, 500, accounts, 'long_a_short_b'))
    await act(() => vi.advanceTimersByTimeAsync(1))
    expect(view.result.current.plan?.executable).toBe(true)
    await act(() => vi.advanceTimersByTimeAsync(10_000))
    expect(view.result.current.plan).toBeNull()
    expect(view.result.current.error).toBeNull()
    expect(view.result.current.marketDataPending).toBe(true)
    await act(() => vi.advanceTimersByTimeAsync(10_000))
    expect(view.result.current.plan?.executable).toBe(true)
    expect(view.result.current.marketDataPending).toBe(false)
    expect(view.result.current.error).toBeNull()
    for (const [, init] of mocks.fetch.mock.calls) {
      expect(JSON.parse(init.body)).toEqual({ opportunity_id: 'opp', leverage: 2, requested_notional: 500, accounts })
    }
  })

  it('preserves actionable plan errors that are not market availability transitions', async () => {
    vi.useFakeTimers()
    mocks.fetch.mockResolvedValueOnce(Response.json({ error: 'internal leverage validation detail', pair_max_leverage: 2 }, { status: 422 }))
    const view = renderHook(() => usePlan('opp', 5, 500))
    await act(() => vi.advanceTimersByTimeAsync(1))
    expect(view.result.current.plan).toBeNull()
    expect(view.result.current.error).toBe('Choose leverage up to 2x.')
    expect(view.result.current.marketDataPending).toBe(false)
  })

  it('does not expose unrecognized backend details in the preview', async () => {
    vi.useFakeTimers()
    mocks.fetch.mockResolvedValueOnce(Response.json({ error: 'internal Phoenix projection revision mismatch' }, { status: 422 }))
    const view = renderHook(() => usePlan('opp', 2, 500))
    await act(() => vi.advanceTimersByTimeAsync(1))
    expect(view.result.current.error).toBe('Unable to load trade.')
    expect(view.result.current.plan).toBeNull()
  })

  it('does not expose a response parser exception in the preview', async () => {
    vi.useFakeTimers()
    mocks.fetch.mockResolvedValueOnce(new Response('not a JSON plan'))
    const view = renderHook(() => usePlan('opp', 2, 500))
    await act(() => vi.advanceTimersByTimeAsync(1))
    expect(view.result.current.error).toBe('Unable to load trade.')
    expect(view.result.current.plan).toBeNull()
  })

  it.each([429, 503])('silently retries a temporary HTTP %s preview failure', async (status) => {
    vi.useFakeTimers()
    mocks.fetch.mockResolvedValueOnce(Response.json({ error: 'internal provider diagnostic' }, { status }))
      .mockResolvedValueOnce(response(true))
    const view = renderHook(() => usePlan('opp', 2, 500))
    await act(() => vi.advanceTimersByTimeAsync(1))
    expect(view.result.current.error).toBeNull()
    expect(view.result.current.marketDataPending).toBe(true)
    await act(() => vi.advanceTimersByTimeAsync(10_000))
    expect(view.result.current.plan?.executable).toBe(true)
    expect(view.result.current.marketDataPending).toBe(false)
  })

  it('retries unavailable depth automatically and pauses in hidden tabs', async () => {
    vi.useFakeTimers()
    mocks.fetch.mockResolvedValueOnce(response(false)).mockResolvedValueOnce(response(true))
    const view = renderHook(() => usePlan('opp', 2, 500, undefined, 'long_a_short_b'))
    await act(() => vi.advanceTimersByTimeAsync(1))
    expect(view.result.current.plan?.executable).toBe(false)
    await act(() => vi.advanceTimersByTimeAsync(10_000))
    expect(mocks.fetch).toHaveBeenCalledTimes(2)
    expect(view.result.current.plan?.executable).toBe(true)
    mocks.visible = false
    view.rerender()
    await act(() => vi.advanceTimersByTimeAsync(20_000))
    expect(mocks.fetch).toHaveBeenCalledTimes(2)
  })

  it('hides a previous-size plan immediately and rejects a mismatched funding direction', async () => {
    vi.useFakeTimers()
    mocks.fetch.mockResolvedValueOnce(response(true)).mockResolvedValueOnce(response(true, 600, 'long_b_short_a'))
    const view = renderHook(({ size }) => usePlan('opp', 2, size, undefined, 'long_a_short_b'), { initialProps: { size: 500 } })
    await act(() => vi.advanceTimersByTimeAsync(1))
    expect(view.result.current.plan?.notional).toBe(500)
    view.rerender({ size: 600 })
    expect(view.result.current.plan).toBeNull()
    expect(view.result.current.loading).toBe(true)
    await act(() => vi.advanceTimersByTimeAsync(1))
    expect(view.result.current.plan).toBeNull()
    expect(view.result.current.error).toMatch(/direction changed/)
  })
})
