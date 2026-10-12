import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import { useOpportunityLiquidity } from '../src/hooks/useOpportunityLiquidity'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), visible: true }))
vi.mock('@/lib/api', () => ({ apiFetch: mocks.fetch }))
vi.mock('@/hooks/usePageVisibility', () => ({ usePageVisibility: () => mocks.visible }))

afterEach(() => { cleanup(); mocks.fetch.mockReset(); mocks.visible = true })

describe('selected opportunity liquidity', () => {
  it('requests only an enabled selected view and pauses when hidden', async () => {
    mocks.fetch.mockResolvedValue(new Response(JSON.stringify({ available: true, capacity: 5000, direction: 'long_a_short_b' })))
    const view = renderHook(({ enabled }) => useOpportunityLiquidity('BTC-aster-hyperliquid', 'long_a_short_b', enabled), { initialProps: { enabled: false } })
    expect(mocks.fetch).not.toHaveBeenCalled()
    view.rerender({ enabled: true })
    await waitFor(() => expect(view.result.current?.capacity).toBe(5000))
    expect(mocks.fetch).toHaveBeenCalledTimes(1)
    expect(mocks.fetch.mock.calls[0][0]).toBe('/api/v1/opportunities/BTC-aster-hyperliquid/liquidity')
    const signal: AbortSignal = mocks.fetch.mock.calls[0][1].signal
    mocks.visible = false
    view.rerender({ enabled: true })
    expect(signal.aborted).toBe(true)
    expect(mocks.fetch).toHaveBeenCalledTimes(1)
  })

  it('hides the old estimate when switching opportunity or receiving unavailable data', async () => {
    mocks.fetch.mockResolvedValueOnce(new Response(JSON.stringify({ available: true, capacity: 5000, direction: 'long_a_short_b' })))
    mocks.fetch.mockResolvedValueOnce(new Response(JSON.stringify({ available: false })))
    const view = renderHook(({ id }) => useOpportunityLiquidity(id, 'long_a_short_b', true), { initialProps: { id: 'BTC' } })
    await waitFor(() => expect(view.result.current?.capacity).toBe(5000))
    view.rerender({ id: 'ETH' })
    expect(view.result.current).toBeNull()
    await waitFor(() => expect(mocks.fetch).toHaveBeenCalledTimes(2))
    expect(view.result.current).toBeNull()
  })
})
