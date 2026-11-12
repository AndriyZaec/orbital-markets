import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { TradingAgentProvider } from '../src/agents/TradingAgentProvider'
import { useTradingAgents } from '../src/hooks/useTradingAgents'

const mocks = vi.hoisted(() => ({ evm: null as string | null,
  restore: vi.fn(async (venue: string, owner: string) => ({ venue, ownerAddress: owner, agentAddress: `${venue}-agent` })),
  phoenixRestore: vi.fn(async () => ({ agentAddress: 'phoenix-agent' })),
}))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: true, publicKey: { toBase58: () => 'SolanaOwner' } }) }))
vi.mock('wagmi', () => ({ useAccount: () => ({ isConnected: !!mocks.evm, address: mocks.evm }),
  useDisconnect: () => ({}), useSignTypedData: () => ({}), useSwitchChain: () => ({}),
}))
vi.mock('@/agents/storage', () => ({ createTradingAgentStore: () => ({ restore: mocks.restore }) }))
vi.mock('@/agents/builder-config', () => ({ asterBuilderAddress: null, hyperliquidBuilderAddress: null }))
vi.mock('@/agents/phoenix-agent', () => ({ restorePhoenixAgent: mocks.phoenixRestore, createPhoenixTransport: () => ({}) }))
vi.mock('@/agents/aster-agent', () => ({ reconcilePendingAsterAgent: async () => null }))

function Consumer() {
  const agents = useTradingAgents()
  return <span>{agents.pacifica.status}/{agents.phoenix.status}/{agents.hyperliquid.status}/{agents.aster.status}</span>
}
afterEach(cleanup)

it('restores only the changed wallet owners and preserves already authorized venues', async () => {
  const view = render(<TradingAgentProvider><Consumer /></TradingAgentProvider>)
  await waitFor(() => expect(screen.getByText('ready/ready/missing/missing')).toBeTruthy())
  expect(mocks.restore.mock.calls.filter(([venue]) => venue === 'pacifica')).toHaveLength(1)
  expect(mocks.phoenixRestore).toHaveBeenCalledTimes(1)
  mocks.evm = '0xEvmOwner'
  await act(async () => view.rerender(<TradingAgentProvider><Consumer /></TradingAgentProvider>))
  await waitFor(() => expect(screen.getByText('ready/ready/ready/ready')).toBeTruthy())
  expect(mocks.restore.mock.calls.filter(([venue]) => venue === 'pacifica')).toHaveLength(1)
  expect(mocks.phoenixRestore).toHaveBeenCalledTimes(1)
})
