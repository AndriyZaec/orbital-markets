import type { ReactNode } from 'react'
import { cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { VenueReadinessProvider, useVenueReadiness } from '../src/hooks/useVenueReadiness'

const mocks = vi.hoisted(() => ({
  apiFetch: vi.fn(), refetch: vi.fn(), owner: 'SolanaOwner', agentOwner: 'SolanaOwner',
  liveReady: false,
}))
vi.mock('@/lib/api', async (original) => ({ ...await original<typeof import('../src/lib/api')>(), apiFetch: mocks.apiFetch }))
vi.mock('@/lib/analytics', () => ({ trackAnalytics: vi.fn() }))
vi.mock('@/hooks/useVenueAuthority', () => ({ useVenueAuthority: () => ({
  pacifica: { address: mocks.owner, readiness: 'ready' },
  phoenix: { address: mocks.owner, readiness: 'ready' },
  hyperliquid: { address: null, readiness: 'not_connected' },
  aster: { address: null, readiness: 'not_connected' },
}) }))
vi.mock('@/hooks/useTradingAgents', () => ({ useTradingAgents: () => ({
  pacifica: { status: 'ready', ownerAddress: mocks.owner, agentAddress: 'pac-agent' },
  phoenix: { status: 'ready', ownerAddress: mocks.agentOwner, agentAddress: 'phoenix-agent' },
  hyperliquid: { status: 'missing' }, aster: { status: 'missing' },
}) }))
vi.mock('@/hooks/useLiveBalances', () => ({ useLiveBalances: () => ({
  pacifica: { connected: true, stream_ready: true, fresh: true, equity: 100, available: 80 },
  phoenix: { connected: true, stream_ready: true, fresh: true, equity: 200, available: 180,
    phoenix: { live_ready: mocks.liveReady, live_unavailable_reason: mocks.liveReady ? undefined : 'Phoenix Flight registration not ready' } },
  hyperliquid: { connected: false }, aster: { connected: false }, refetch: mocks.refetch,
}) }))

function wrapper({ children }: { children: ReactNode }) { return <VenueReadinessProvider>{children}</VenueReadinessProvider> }
beforeEach(() => {
  mocks.apiFetch.mockReset().mockResolvedValue(Response.json({}))
  mocks.refetch.mockReset().mockResolvedValue(undefined)
  mocks.owner = 'SolanaOwner'; mocks.agentOwner = 'SolanaOwner'; mocks.liveReady = false
})
afterEach(cleanup)

it('requires explicit server live readiness and a case-sensitive matching local Phoenix owner', () => {
  const { result, rerender } = renderHook(useVenueReadiness, { wrapper })
  expect(result.current.phoenix.status).toBe('unavailable')
  expect(result.current.phoenix.blockingReasons).toContain('Phoenix Flight registration not ready')
  expect(result.current.aggregate.readyCount).toBe(1)
  mocks.liveReady = true; rerender()
  expect(result.current.phoenix.status).toBe('ready')
  expect(result.current.aggregate.readyCount).toBe(2)
  mocks.agentOwner = 'solanaowner'; rerender()
  expect(result.current.phoenix.status).toBe('agent_missing')
})

it('starts the existing account streams when optional Phoenix infrastructure is unavailable', async () => {
  mocks.apiFetch.mockImplementation(async (_path, init) => JSON.parse(init.body).accounts.phoenix
    ? Response.json({ error: 'Phoenix unavailable' }, { status: 503 }) : Response.json({}))
  const { result } = renderHook(useVenueReadiness, { wrapper })
  await waitFor(() => expect(result.current.ensureStatus).toBe('ready'))
  const accounts = mocks.apiFetch.mock.calls.map(([, init]) => JSON.parse(init.body).accounts)
  expect(accounts).toContainEqual({ pacifica: 'SolanaOwner' })
  expect(accounts).toContainEqual({ phoenix: 'SolanaOwner' })
  expect(result.current.pacifica.status).toBe('ready')
  expect(result.current.phoenix.status).toBe('unavailable')
})
