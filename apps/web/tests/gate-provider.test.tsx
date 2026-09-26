// @vitest-environment jsdom

import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { GateProvider } from '../src/providers/GateProvider'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('checks access without loading opportunities', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetch)

  render(<GateProvider><span>Trading terminal</span></GateProvider>)

  await waitFor(() => expect(screen.getByText('Trading terminal')).toBeTruthy())
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/access')
})
