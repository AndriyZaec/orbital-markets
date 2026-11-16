import type { ReactNode } from 'react'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { venueAccountsKey } from '../src/accounts/venue-accounts-storage'
import { LiveExecutionProvider, useLiveExecution } from '../src/hooks/useLiveExecution'
import { LivePositionsProvider, useLivePositions } from '../src/hooks/useLivePositions'
import type { ExecutionIntent } from '../src/lib/live-execution-state'
import type { SigningRequest } from '../src/types/signing'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), eventUrls: [] as string[] }))
vi.mock('@/lib/api', async (original) => ({ ...await original<typeof import('../src/lib/api')>(), apiFetch: mocks.apiFetch }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: false }) }))
vi.mock('wagmi', () => ({ useAccount: () => ({ isConnected: false }) }))
vi.mock('@/hooks/useTradingAgents', () => ({ useTradingAgents: () => ({
  hyperliquid: { status: 'ready', agentAddress: 'hl-agent' }, aster: { status: 'ready', agentAddress: 'aster-agent' }, pacifica: {}, phoenix: {},
  sign: async (request: SigningRequest) => ({ request_id: request.id, client_order_id: request.client_order_id, venue: request.venue, signer_address: 'agent', signature: 'test' }),
  ensureHyperliquidTradingApproval: async () => {},
}) }))

const accounts = { hyperliquid: '0x1111111111111111111111111111111111111111', aster: '0x2222222222222222222222222222222222222222' }
function request(action: 'open' | 'unwind'): SigningRequest {
  return { id: action, client_order_id: action, venue: 'aster', account: accounts.aster, symbol: 'BTCUSDT', action,
    side: action === 'open' ? 'buy' : 'sell', amount: 1, price: 100.5, limit_price: 100.5, leverage: 2, reduce_only: action === 'unwind',
    unsigned_payload: {}, created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 30_000).toISOString() }
}
function wrapper({ children }: { children: ReactNode }) {
  return <VenueAccountsProvider><LiveExecutionProvider><LivePositionsProvider>{children}</LivePositionsProvider></LiveExecutionProvider></VenueAccountsProvider>
}

beforeEach(() => {
  localStorage.clear(); localStorage.setItem(venueAccountsKey, JSON.stringify(accounts)); mocks.eventUrls = []
  vi.stubGlobal('EventSource', class {
    constructor(url: string) { mocks.eventUrls.push(url) }
    addEventListener() {}
    close() {}
  })
  mocks.apiFetch.mockReset().mockImplementation(async (url: string) => {
    if (url.includes('/live/positions')) return Response.json([])
    if (url === '/api/v1/live/prepare') return Response.json({ session_id: 'session-1', asset: 'BTC', riskier_venue: 'aster', hedge_venue: 'hyperliquid', signing_requests: [request('open'), request('unwind')] })
    if (url === '/api/v1/live/advance') return Response.json({ status: 'recovering', leg1_fill: { filled_amount: .6, avg_price: 100, status: 'filled' } })
    throw new Error(`Unexpected request: ${url}`)
  })
})
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it('uses independent registry addresses in position queries, execution bodies, and session subscriptions without an owner wallet', async () => {
  const { result } = renderHook(() => ({ positions: useLivePositions(), execution: useLiveExecution() }), { wrapper })
  await waitFor(() => expect(mocks.apiFetch).toHaveBeenCalled())
  const positionUrl = mocks.apiFetch.mock.calls.find(([url]) => url.includes('/live/positions'))![0]
  const query = new URL(positionUrl, 'https://orbital.test').searchParams
  expect(query.get('accounts[aster]')).toBe(accounts.aster)
  expect(query.get('accounts[hyperliquid]')).toBe(accounts.hyperliquid)
  const intent: ExecutionIntent = { opportunityId: 'opp', asset: 'BTC', leverage: 2, requestedNotional: 100,
    approvedBaseAmount: 1, maxSlippagePct: .005, expiresAt: new Date(Date.now() + 300_000).toISOString(),
    legs: [{ venue: 'aster', symbol: 'BTCUSDT', side: 'buy', expectedPrice: 100, limitPrice: 100.5 },
      { venue: 'hyperliquid', symbol: 'BTC', side: 'sell', expectedPrice: 100, limitPrice: 99.5 }],
  }
  await act(async () => { await result.current.execution.executeLive(intent) })
  expect(result.current.execution.state.phase).toBe('recovering')
  for (const url of ['/api/v1/live/prepare', '/api/v1/live/advance']) {
    const call = mocks.apiFetch.mock.calls.find(([path]) => path === url)!
    expect(JSON.parse(call[1].body).accounts).toEqual(accounts)
  }
  const sessionUrl = mocks.eventUrls.find((url) => url.includes('session_id=session-1'))!
  expect(sessionUrl).toBeTruthy()
  const sessionQuery = new URL(sessionUrl, 'https://orbital.test').searchParams
  expect(sessionQuery.get('accounts[aster]')).toBe(accounts.aster)
  expect(sessionQuery.get('accounts[hyperliquid]')).toBe(accounts.hyperliquid)
})
