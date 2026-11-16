import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import bs58 from 'bs58'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { useVenueAccounts } from '../src/accounts/useVenueAccounts'
import { TradingAgentProvider } from '../src/agents/TradingAgentProvider'
import { useTradingAgents } from '../src/hooks/useTradingAgents'
import type { StoredTradingAgent } from '../src/agents/types'
import type { SigningRequest } from '../src/types/signing'
import { TestTradingAgentStore } from './trading-agent-test-store'

const accounts = {
  hyperliquid: '0x1111111111111111111111111111111111111111',
  aster: '0x2222222222222222222222222222222222222222',
  pacifica: bs58.encode(new Uint8Array(32).fill(1)),
  phoenix: bs58.encode(new Uint8Array(32).fill(2)),
}
const storage = new TestTradingAgentStore()
vi.mock('@/agents/storage', () => ({ createTradingAgentStore: () => storage }))
vi.mock('@/agents/builder-config', () => ({ asterBuilderAddress: null, hyperliquidBuilderAddress: null }))
vi.mock('@/agents/phoenix-message', () => ({ signPhoenixDelegation: vi.fn() }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: true, publicKey: { toBase58: () => accounts.pacifica } }) }))
vi.mock('wagmi', () => ({ useAccount: () => ({ isConnected: true, address: accounts.hyperliquid }), useConnections: () => [],
  useDisconnect: () => ({}), useSignTypedData: () => ({}), useSwitchChain: () => ({}),
}))
// Keep provider guards and storage lookup real; only replace venue wire signing.
vi.mock('@/agents/aster-agent', () => ({ reconcilePendingAsterAgent: async () => null,
  signAsterAgentRequest: async (request: SigningRequest, agent: StoredTradingAgent) => ({
    request_id: request.id, client_order_id: request.client_order_id, venue: request.venue,
    signer_address: agent.agentAddress, signature: agent.privateKey,
  }),
}))
vi.mock('@/agents/phoenix-order', () => ({ signPhoenixAgentRequest: async (request: SigningRequest, agent: StoredTradingAgent) => ({
  request_id: request.id, client_order_id: request.client_order_id, venue: request.venue,
  signer_address: agent.agentAddress, signature: agent.privateKey,
}) }))

function wrapper({ children }: { children: ReactNode }) {
  return <VenueAccountsProvider><TradingAgentProvider>{children}</TradingAgentProvider></VenueAccountsProvider>
}

function request(venue: 'aster' | 'phoenix', account = accounts[venue]): SigningRequest {
  return { id: 'request-1', client_order_id: 'order-1', venue, account, action: 'close', symbol: 'SOL', side: 'sell',
    amount: 1, price: 100, reduce_only: true, unsigned_payload: {}, expires_at: '2099-01-01', created_at: '2026-01-01' }
}

beforeEach(async () => {
  storage.values.clear()
  for (const [venue, ownerAddress] of Object.entries(accounts)) {
    await storage.save({ version: 2, venue: venue as StoredTradingAgent['venue'], ownerAddress,
      agentAddress: `${venue}-own-agent`, privateKey: `${venue}-own-key`, authorizedAt: '2026-01-01' })
  }
  for (const venue of ['aster', 'phoenix'] as const) {
    await storage.save({ version: 2, venue, ownerAddress: accounts[venue === 'aster' ? 'hyperliquid' : 'pacifica'],
      agentAddress: `${venue}-wrong-agent`, privateKey: 'wrong-key', authorizedAt: '2026-01-01' })
  }
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })

async function independentSession() {
  const view = renderHook(() => ({ registry: useVenueAccounts(), agents: useTradingAgents() }), { wrapper })
  act(() => {
    for (const venue of ['pacifica', 'hyperliquid', 'aster', 'phoenix'] as const) view.result.current.registry.link(venue, accounts[venue])
  })
  await waitFor(() => {
    for (const venue of ['pacifica', 'hyperliquid', 'aster', 'phoenix'] as const) {
      expect(view.result.current.agents[venue]).toMatchObject({ ownerAddress: accounts[venue], agentAddress: `${venue}-own-agent`, status: 'ready' })
    }
  })
  return view
}

it.each(['aster', 'phoenix'] as const)('signs %s with its own registered owner and stored agent, independently of the connected wallet', async (venue) => {
  const { result } = await independentSession()
  const lookup = vi.spyOn(storage, 'loadForSigning')
  await expect(result.current.agents.sign(request(venue))).resolves.toMatchObject({ signer_address: `${venue}-own-agent`, signature: `${venue}-own-key` })
  expect(lookup).toHaveBeenCalledExactlyOnceWith(venue, accounts[venue])
})

it.each(['aster', 'phoenix'] as const)('rejects a %s request using the other venue owner before loading any signing key', async (venue) => {
  const { result } = await independentSession()
  const lookup = vi.spyOn(storage, 'loadForSigning')
  const otherOwner = accounts[venue === 'aster' ? 'hyperliquid' : 'pacifica']
  await expect(result.current.agents.sign(request(venue, otherOwner))).rejects.toThrow(`${venue} owner changed during execution`)
  expect(lookup).not.toHaveBeenCalled()
})
