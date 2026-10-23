import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, renderHook, waitFor, act } from '@testing-library/react'
import { useHistory } from '../src/hooks/useHistory'

const mocks = vi.hoisted(() => ({ fetch: vi.fn() }))
vi.mock('@/lib/api', () => ({
  apiFetch: mocks.fetch,
  apiResponseError: async () => new Error('History unavailable'),
  userErrorMessage: () => 'History unavailable',
}))

afterEach(() => { cleanup(); mocks.fetch.mockReset() })

describe('history series selection', () => {
  it('does not request redundant market history when a position Price PnL chart is active', async () => {
    const view = renderHook(() => useHistory('SOL', 'aster', 'pacifica', '24h', null))
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 1)) })
    expect(mocks.fetch).not.toHaveBeenCalled()
    expect(view.result.current.loading).toBe(false)
  })

  it('requests aligned price history without showing cached funding data during the switch', async () => {
    const point = { t: '2026-10-20T10:00:00Z', basis: 0, edge: 0, funding_a: 0.001, funding_b: 0.002, price_spread: null }
    mocks.fetch.mockResolvedValueOnce(new Response(JSON.stringify({ points: [point] })))
    let resolveSpread!: (response: Response) => void
    mocks.fetch.mockReturnValueOnce(new Promise<Response>((resolve) => { resolveSpread = resolve }))
    const view = renderHook(({ series }: { series: 'funding' | 'price_spread' }) => useHistory('SOL', 'aster', 'pacifica', '24h', series), { initialProps: { series: 'funding' } })
    await waitFor(() => expect(view.result.current.loading).toBe(false))
    expect(view.result.current.data).toEqual([point])
    view.rerender({ series: 'price_spread' })
    expect(view.result.current.data).toEqual([])
    expect(view.result.current.loading).toBe(true)
    await waitFor(() => expect(mocks.fetch).toHaveBeenLastCalledWith('/api/v1/history?asset=SOL&venue_a=aster&venue_b=pacifica&range=24h&series=price_spread'))
    await act(async () => resolveSpread(new Response(JSON.stringify({ points: [{ ...point, price_spread: 0.003 }], start: '2026-10-19T10:04:00Z', end: '2026-10-20T10:04:00Z' }))))
    expect(view.result.current.data[0].price_spread).toBe(0.003)
    expect(view.result.current.window?.end).toBe('2026-10-20T10:04:00Z')
  })
})
