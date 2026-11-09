import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, type RenderOptions } from '@testing-library/react'
import { afterEach } from 'vitest'

const clients = new Set<QueryClient>()
afterEach(() => {
  cleanup()
  for (const client of clients) client.clear()
  clients.clear()
})

export function renderWithQueryClient(ui: ReactNode, options: RenderOptions = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  clients.add(client)
  const ExistingWrapper = options.wrapper
  return render(ui, {
    ...options,
    wrapper: ({ children }) => <QueryClientProvider client={client}>
      {ExistingWrapper ? <ExistingWrapper>{children}</ExistingWrapper> : children}
    </QueryClientProvider>,
  })
}
