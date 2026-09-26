// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { useOpportunities } from '../src/hooks/useOpportunities'

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('bounds follow-up polling for a stale signal snapshot', async () => {
  vi.useFakeTimers()
  const payload = JSON.stringify([{
    id: 'SOL-pacifica-hyperliquid',
    signal_7d_state: 'stale',
    signal_7d_version: 1,
  }])
  const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response(payload, {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })))
  vi.stubGlobal('fetch', fetch)

  renderHook(() => useOpportunities(60_000))
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(fetch).toHaveBeenCalledTimes(1)

  await act(async () => { await vi.advanceTimersByTimeAsync(15_000) })
  expect(fetch).toHaveBeenCalledTimes(4)

  await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
  expect(fetch).toHaveBeenCalledTimes(4)
})

it('allows a follow-up response to finish before rotating its poll timer', async () => {
  vi.useFakeTimers()
  const payload = JSON.stringify([{
    id: 'SOL-pacifica-hyperliquid',
    signal_7d_state: 'refreshing',
    signal_7d_version: 1,
  }])
  let calls = 0
  let abortsBeforeCompletion = 0
  const fetch = vi.fn().mockImplementation((_input: RequestInfo | URL, init?: RequestInit) => {
    calls += 1
    if (calls === 1) return Promise.resolve(new Response(payload, { status: 200 }))
    return new Promise<Response>((resolve, reject) => {
      let completed = false
      const timer = window.setTimeout(() => {
        completed = true
        resolve(new Response(payload, { status: 200 }))
      }, 100)
      init?.signal?.addEventListener('abort', () => {
        if (!completed) abortsBeforeCompletion += 1
        window.clearTimeout(timer)
        reject(new DOMException('Aborted', 'AbortError'))
      }, { once: true })
    })
  })
  vi.stubGlobal('fetch', fetch)

  renderHook(() => useOpportunities(60_000))
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })

  expect(fetch).toHaveBeenCalledTimes(2)
  expect(abortsBeforeCompletion).toBe(0)
  await act(async () => { await vi.advanceTimersByTimeAsync(100) })
  expect(abortsBeforeCompletion).toBe(0)
})

it('loads the shared opportunity snapshot without account query parameters', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('[]', {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }))
  vi.stubGlobal('fetch', fetch)

  const { result } = renderHook(() => useOpportunities(60_000))

  await waitFor(() => expect(result.current.loading).toBe(false))
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/opportunities')
})
