import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { AssetIcon } from '../src/components/AssetIcon'

const clients: QueryClient[] = []
afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  vi.unstubAllGlobals()
})

function client() {
  const queryClient = new QueryClient()
  clients.push(queryClient)
  return queryClient
}

it('loads one shared Phoenix catalog only after existing icons fail', async () => {
  let resolve!: (response: Response) => void
  const fetch = vi.fn(() => new Promise<Response>(done => { resolve = done }))
  vi.stubGlobal('fetch', fetch)
  const queryClient = client()
  const view = render(<QueryClientProvider client={queryClient}>
    <AssetIcon asset="DELL" /><AssetIcon asset="GOLD" />
  </QueryClientProvider>)
  const initial = view.container.querySelectorAll('img')
  expect(initial[0].getAttribute('src')).toBe('https://app.hyperliquid.xyz/coins/DELL.svg')
  expect(fetch).not.toHaveBeenCalled()
  fireEvent.error(initial[0])
  fireEvent.error(initial[1])
  expect(fetch).toHaveBeenCalledTimes(1)
  await act(async () => resolve(new Response(JSON.stringify({ markets: [
    { symbol: 'DELL', metadata: { logoUri: 'https://phoenixcdn.trade/logos/DELL/example.svg' } },
    { symbol: 'GOLD', metadata: { logoUri: 'https://phoenixcdn.trade/logos/GOLD/example.svg' } },
  ] }))))
  await waitFor(() => {
    const icons = view.container.querySelectorAll('img')
    expect(icons[0]?.getAttribute('src')).toBe('https://phoenixcdn.trade/logos/DELL/example.svg')
    expect(icons[1]?.getAttribute('src')).toBe('https://phoenixcdn.trade/logos/GOLD/example.svg')
  })
  // A failed optional Phoenix image ends in the existing text fallback.
  fireEvent.error(view.container.querySelector('img')!)
  expect(screen.getByText('DE')).toBeTruthy()
  expect(fetch).toHaveBeenCalledTimes(1)

  view.rerender(<QueryClientProvider client={queryClient}><AssetIcon asset="BTC" /></QueryClientProvider>)
  expect(view.container.querySelector('img')?.getAttribute('src')).toBe('https://app.hyperliquid.xyz/coins/BTC.svg')
  expect(fetch).toHaveBeenCalledTimes(1)
})

it.each(['HTTP failure', 'network failure'])('caches %s so later missing icons do not retry per row', async failure => {
  const fetch = vi.fn().mockImplementation(async () => {
    if (failure === 'network failure') throw new Error('Offline')
    return new Response('', { status: 503 })
  })
  vi.stubGlobal('fetch', fetch)
  const queryClient = client()
  const view = render(<QueryClientProvider client={queryClient}><AssetIcon asset="DELL" /></QueryClientProvider>)
  fireEvent.error(view.container.querySelector('img')!)
  await waitFor(() => expect(queryClient.getQueryData(['phoenix-asset-icons'])).toEqual({}))
  expect(screen.getByText('DE')).toBeTruthy()
  view.rerender(<QueryClientProvider client={queryClient}><AssetIcon asset="GOLD" /></QueryClientProvider>)
  fireEvent.error(view.container.querySelector('img')!)
  expect(screen.getByText('GO')).toBeTruthy()
  expect(fetch).toHaveBeenCalledTimes(1)
})

it('keeps Pacifica before Phoenix and uses the cached catalog for a later icon', async () => {
  const queryClient = client()
  const phoenixURL = 'https://phoenixcdn.trade/logos/BP/example.svg'
  queryClient.setQueryData(['phoenix-asset-icons'], { BP: phoenixURL })
  const fetch = vi.fn()
  vi.stubGlobal('fetch', fetch)
  const view = render(<QueryClientProvider client={queryClient}><AssetIcon asset="BP" /></QueryClientProvider>)
  fireEvent.error(view.container.querySelector('img')!)
  expect(view.container.querySelector('img')?.getAttribute('src')).toContain('app.pacifica.fi/')
  fireEvent.error(view.container.querySelector('img')!)
  await waitFor(() => expect(view.container.querySelector('img')?.getAttribute('src')).toBe(phoenixURL))
  expect(fetch).not.toHaveBeenCalled()
})
