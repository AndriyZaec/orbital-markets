import { act, cleanup, renderHook, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { venueAccountsKey } from '../src/accounts/venue-accounts-storage'
import { TradingAgentProvider } from '../src/agents/TradingAgentProvider'
import { asterBuilderAddress, hyperliquidBuilderAddress } from '../src/agents/builder-config'
import { useTradingAgents } from '../src/hooks/useTradingAgents'
import { LiveExecutionProvider, useLiveExecution } from '../src/hooks/useLiveExecution'
import type { ExecutionIntent } from '../src/lib/live-execution-state'
import type { SigningRequest } from '../src/types/signing'
import { TestTradingAgentStore } from './trading-agent-test-store'
import { LiveExecutionModal } from '../src/components/LiveExecutionModal'
import { renderWithQueryClient } from './render-with-query-client'

const mocks = vi.hoisted(() => ({ connected: true, signTypedData: vi.fn(), apiFetch: vi.fn() }))
const storage = new TestTradingAgentStore()
const owner = '0x1111111111111111111111111111111111111111'
const asterOwner = '0x2222222222222222222222222222222222222222'
const signature = `0x${'1'.repeat(64)}${'2'.repeat(64)}1b`
vi.mock('@/agents/storage', () => ({ createTradingAgentStore: () => storage }))
vi.mock('@/agents/aster-agent', () => ({ reconcilePendingAsterAgent: async () => null }))
vi.mock('@/lib/api', async (original) => ({ ...await original<typeof import('../src/lib/api')>(), apiFetch: mocks.apiFetch }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: false }) }))
vi.mock('wagmi', () => ({
  useAccount: () => ({ isConnected: mocks.connected, address: mocks.connected ? owner : undefined, chainId: 1 }),
  useConnections: () => [], useDisconnect: () => ({}), useSwitchChain: () => ({ switchChainAsync: vi.fn() }),
  useSignTypedData: () => ({ signTypedDataAsync: mocks.signTypedData }),
}))

function wrapper({ children }: { children: ReactNode }) {
  return <VenueAccountsProvider><TradingAgentProvider><LiveExecutionProvider>{children}</LiveExecutionProvider></TradingAgentProvider></VenueAccountsProvider>
}
function session() { return renderHook(() => ({ agents: useTradingAgents(), execution: useLiveExecution() }), { wrapper }) }

beforeEach(() => {
  localStorage.clear(); storage.values.clear(); mocks.connected = true
  localStorage.setItem(venueAccountsKey, JSON.stringify({ hyperliquid: owner, aster: asterOwner }))
  mocks.signTypedData.mockReset().mockResolvedValue(signature)
  mocks.apiFetch.mockReset().mockImplementation(async (path: string) => path.endsWith('/approval-config')
    ? Response.json({ agent_name: 'Orbital Markets' }) : Response.json({}))
  vi.stubGlobal('fetch', vi.fn(async () => Response.json(0))) // No builder allowance yet.
})
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('approves Hyperliquid trading fees after the agent approval during Authorize', async () => {
  const { result } = session()
  await act(async () => { await result.current.agents.authorize('hyperliquid') })
  expect(mocks.signTypedData.mock.calls.map(([typedData]) => typedData.primaryType)).toEqual([
    'HyperliquidTransaction:ApproveAgent', 'HyperliquidTransaction:ApproveBuilderFee',
  ])
  const posts = mocks.apiFetch.mock.calls.filter(([, init]) => init?.method === 'POST').map(([path]) => path)
  expect(posts).toEqual(['/api/v1/live/agents/hyperliquid/approve', '/api/v1/live/agents/hyperliquid/approve-builder-fee'])
  expect(result.current.agents.hyperliquid).toMatchObject({ status: 'ready', error: null })
})

it.each(['cancelled', 'network error'])('keeps the successfully authorized agent ready after fee approval %s', async (failure) => {
  if (failure === 'cancelled') mocks.signTypedData.mockImplementation(async (typedData) => {
    if (typedData.primaryType === 'HyperliquidTransaction:ApproveBuilderFee') throw Object.assign(new Error('Cancelled'), { code: 4001 })
    return signature
  })
  else vi.mocked(fetch).mockRejectedValue(new TypeError('Failed to fetch'))
  const { result } = session()
  await act(async () => { await result.current.agents.authorize('hyperliquid') })
  expect(result.current.agents.hyperliquid).toMatchObject({ status: 'ready', error: null })
  expect(await storage.loadForSigning('hyperliquid', owner)).toMatchObject({ agentAddress: result.current.agents.hyperliquid.agentAddress })
})

it('stops before signing or submitting leg one when Hyperliquid is leg two and fees need the disconnected owner wallet', async () => {
  mocks.connected = false
  for (const [venue, ownerAddress, builderAddress] of [
    ['hyperliquid', owner, hyperliquidBuilderAddress], ['aster', asterOwner, asterBuilderAddress],
  ] as const) await storage.save({ version: 2, venue, ownerAddress, builderAddress, agentAddress: `${venue}-agent`, privateKey: 'test-key', authorizedAt: '2026-01-01' })
  const requests: SigningRequest[] = (['open', 'unwind'] as const).map((action) => ({
    id: action, client_order_id: action, venue: 'aster', account: asterOwner, symbol: 'BTCUSDT', action,
    side: action === 'open' ? 'buy' : 'sell', amount: 1, price: 100.5, limit_price: 100.5, leverage: 2, reduce_only: action === 'unwind',
    unsigned_payload: {}, created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 30_000).toISOString(),
  }))
  mocks.apiFetch.mockResolvedValue(Response.json({ session_id: 'session', asset: 'BTC', riskier_venue: 'aster', hedge_venue: 'hyperliquid', signing_requests: requests }))
  const { result } = session()
  await waitFor(() => expect(result.current.agents.aster.status).toBe('ready'))
  const lookup = vi.spyOn(storage, 'loadForSigning')
  const intent: ExecutionIntent = { opportunityId: 'opp', asset: 'BTC', leverage: 2, requestedNotional: 100,
    approvedBaseAmount: 1, maxSlippagePct: .005, expiresAt: new Date(Date.now() + 300_000).toISOString(),
    legs: [{ venue: 'aster', symbol: 'BTCUSDT', side: 'buy', expectedPrice: 100, limitPrice: 100.5 },
      { venue: 'hyperliquid', symbol: 'BTC', side: 'sell', expectedPrice: 100, limitPrice: 99.5 }],
  }
  await act(async () => { await result.current.execution.executeLive(intent) })
  expect(result.current.execution.state).toMatchObject({ phase: 'failed', notStarted: true,
    userMessage: 'Connect your Hyperliquid wallet to approve trading fees.',
    error: 'Connect your Hyperliquid wallet to approve trading fees.',
  })
  expect(lookup).not.toHaveBeenCalled()
  expect(mocks.signTypedData).not.toHaveBeenCalled()
  expect(mocks.apiFetch.mock.calls.map(([path]) => path)).toEqual(['/api/v1/live/prepare'])
  renderWithQueryClient(<LiveExecutionModal state={result.current.execution.state} onRetry={vi.fn()} onClose={vi.fn()} onViewPositions={vi.fn()} />)
  expect(screen.getByText('Trade not started')).toBeTruthy()
  expect(screen.getByText('No orders were submitted.')).toBeTruthy()
  expect(screen.getByText('Connect your Hyperliquid wallet to approve trading fees.')).toBeTruthy()
})
