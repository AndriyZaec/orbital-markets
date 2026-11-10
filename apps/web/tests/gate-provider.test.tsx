// @vitest-environment jsdom

import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { useEffect } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { GateProvider } from '../src/providers/GateProvider'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('does not destroy account session owners on transient focus probe failures', async () => {
  const mounted = vi.fn(), unmounted = vi.fn()
  function Session() {
    useEffect(() => { mounted(); return unmounted }, [])
    return <span>Connected accounts</span>
  }
  const fetch = vi.fn().mockResolvedValueOnce(new Response(null, { status: 204 }))
    .mockRejectedValueOnce(new TypeError('offline'))
    .mockResolvedValueOnce(new Response(null, { status: 503 }))
    .mockResolvedValueOnce(new Response(null, { status: 404 }))
  vi.stubGlobal('fetch', fetch)
  render(<GateProvider><Session /></GateProvider>)
  await waitFor(() => expect(screen.getByText('Connected accounts')).toBeTruthy())
  await act(async () => window.dispatchEvent(new Event('focus')))
  await act(async () => window.dispatchEvent(new Event('focus')))
  expect(mounted).toHaveBeenCalledTimes(1)
  expect(unmounted).not.toHaveBeenCalled()
  await act(async () => window.dispatchEvent(new Event('focus')))
  expect(unmounted).toHaveBeenCalledTimes(1)
})

it('checks access without loading opportunities', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetch)

  render(<GateProvider><span>Trading terminal</span></GateProvider>)

  await waitFor(() => expect(screen.getByText('Trading terminal')).toBeTruthy())
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/access')
})
