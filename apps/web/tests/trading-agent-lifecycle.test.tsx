import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { TradingAgentProvider } from '../src/agents/TradingAgentProvider'
import { useTradingAgents } from '../src/hooks/useTradingAgents'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { useVenueAccounts } from '../src/accounts/useVenueAccounts'
import { venueAccountsKey } from '../src/accounts/venue-accounts-storage'

const mocks = vi.hoisted(() => ({ evm: null as string | null,
  restore: vi.fn(async (venue: string, owner: string) => ({ venue, ownerAddress: owner, agentAddress: `${venue}-agent` })),
  phoenixRestore: vi.fn(async () => ({ agentAddress: 'phoenix-agent' })),
}))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: true, publicKey: { toBase58: () => '11111111111111111111111111111111' } }) }))
vi.mock('wagmi', () => ({ useAccount: () => ({ isConnected: !!mocks.evm, address: mocks.evm }),
  useConnections: () => [],
  useDisconnect: () => ({}), useSignTypedData: () => ({}), useSwitchChain: () => ({}),
}))
vi.mock('@/agents/storage', () => ({ createTradingAgentStore: () => ({ restore: mocks.restore }) }))
vi.mock('@/agents/builder-config', () => ({ asterBuilderAddress: null, hyperliquidBuilderAddress: null }))
vi.mock('@/agents/phoenix-agent', () => ({ restorePhoenixAgent: mocks.phoenixRestore, createPhoenixTransport: () => ({}) }))
vi.mock('@/agents/aster-agent', () => ({ reconcilePendingAsterAgent: async () => null }))

function Consumer() {
  const agents = useTradingAgents()
  const { link } = useVenueAccounts()
  return <><span>{agents.pacifica.status}/{agents.phoenix.status}/{agents.hyperliquid.status}/{agents.aster.status}</span>
    <button onClick={() => { link('hyperliquid', mocks.evm!); link('aster', mocks.evm!) }}>Link EVM</button></>
}
afterEach(cleanup)

it('restores only the changed wallet owners and preserves already authorized venues', async () => {
  localStorage.setItem(venueAccountsKey, JSON.stringify({ pacifica: '11111111111111111111111111111111', phoenix: '11111111111111111111111111111111' }))
  render(<TradingAgentProvider><Consumer /></TradingAgentProvider>, { wrapper: VenueAccountsProvider })
  await waitFor(() => expect(screen.getByText('ready/ready/missing/missing')).toBeTruthy())
  expect(mocks.restore.mock.calls.filter(([venue]) => venue === 'pacifica')).toHaveLength(1)
  expect(mocks.phoenixRestore).toHaveBeenCalledTimes(1)
  mocks.evm = '0x1111111111111111111111111111111111111111'
  await act(async () => screen.getByRole('button', { name: 'Link EVM' }).click())
  await waitFor(() => expect(screen.getByText('ready/ready/ready/ready')).toBeTruthy())
  expect(mocks.restore.mock.calls.filter(([venue]) => venue === 'pacifica')).toHaveLength(1)
  expect(mocks.phoenixRestore).toHaveBeenCalledTimes(1)
})
