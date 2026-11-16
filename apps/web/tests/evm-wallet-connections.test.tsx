import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import { createConfig, http, useAccount, WagmiProvider, type Config } from 'wagmi'
import { connect, getConnections } from 'wagmi/actions'
import { mainnet } from 'wagmi/chains'
import { mock } from 'wagmi/connectors'
import { TradingAgentProvider } from '../src/agents/TradingAgentProvider'
import { ConnectAccounts } from '../src/components/ConnectAccounts'
import { useTradingAgents } from '../src/hooks/useTradingAgents'
import { WalletSyncedVenueAccounts } from './venue-accounts-test-provider'

const storage = vi.hoisted(() => ({
  restore: vi.fn(async () => null),
  clear: vi.fn(async () => {}),
}))
vi.mock('@/agents/storage', () => ({ createTradingAgentStore: () => storage }))
vi.mock('@/agents/aster-agent', () => ({ reconcilePendingAsterAgent: async () => null }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: false, publicKey: null }) }))
vi.mock('@solana/wallet-adapter-react-ui', () => ({ useWalletModal: () => ({ setVisible: vi.fn() }) }))
vi.mock('@/lib/analytics', () => ({ trackAnalytics: vi.fn() }))
vi.mock('@/hooks/useLiveExecution', () => ({ useLiveExecution: () => ({ state: { phase: 'idle' } }) }))
vi.mock('@/hooks/useLivePositions', () => ({ useLivePositions: () => ({ positions: [] }) }))
vi.mock('@/hooks/useTelegramLink', () => ({ useTelegramLink: () => ({}) }))
// Account network reads are outside this test; wallet owners and agent state are real.
vi.mock('@/hooks/useVenueReadiness', async () => {
  const { useTradingAgents } = await import('../src/hooks/useTradingAgents')
  const refreshBalances = async () => {}
  return { useVenueReadiness: () => {
    const agents = useTradingAgents()
    const readiness = (venue: 'pacifica' | 'phoenix' | 'hyperliquid' | 'aster') => {
      const agent = agents[venue]
      return { venue, address: agent.ownerAddress, shortAddress: agent.ownerAddress,
        walletConnected: !!agent.ownerAddress, signerReady: !!agent.ownerAddress,
        agentStatus: agent.status, agentReady: false, blockingReasons: [],
        status: agent.ownerAddress ? 'agent_missing' : 'disconnected',
      }
    }
    return { pacifica: readiness('pacifica'), phoenix: readiness('phoenix'),
      hyperliquid: readiness('hyperliquid'), aster: readiness('aster'),
      aggregate: { readyCount: 0, totalCount: 4, statusLabel: 'Not connected' }, refreshBalances }
  } }
})

const ownerA = '0x1111111111111111111111111111111111111111'
const ownerB = '0x2222222222222222222222222222222222222222'

function walletConfig(singleConnector = false) {
  return createConfig({ chains: [mainnet], transports: { [mainnet.id]: http() },
    connectors: singleConnector ? [mock({ accounts: [ownerB] })] : [mock({ accounts: [ownerA] }), mock({ accounts: [ownerB] })],
    storage: null, multiInjectedProviderDiscovery: false,
  })
}

function WalletState() {
  const agents = useTradingAgents()
  const account = useAccount()
  return <>
    <output aria-label="Hyperliquid owner">{agents.hyperliquid.ownerAddress ?? 'none'}</output>
    <output aria-label="Aster owner">{agents.aster.ownerAddress ?? 'none'}</output>
    <output aria-label="Wagmi status">{account.status}</output>
  </>
}

function renderAccounts(config: Config) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<WagmiProvider config={config} reconnectOnMount={false}>
    <QueryClientProvider client={queryClient}>
      <WalletSyncedVenueAccounts>
        <TradingAgentProvider><WalletState /><ConnectAccounts open onClose={() => {}} /></TradingAgentProvider>
      </WalletSyncedVenueAccounts>
    </QueryClientProvider>
  </WagmiProvider>)
}

afterEach(() => { cleanup(); vi.clearAllMocks() })

it.each(['Hyperliquid', 'Aster'])('Disconnect from %s clears both EVM connections instead of revealing the other wallet', async (venue) => {
  const config = walletConfig()
  await connect(config, { connector: config.connectors[0] })
  await connect(config, { connector: config.connectors[1] })
  expect(getConnections(config)).toHaveLength(2)
  for (const connector of config.connectors) {
    const disconnect = connector.disconnect.bind(connector)
    vi.spyOn(connector, 'disconnect').mockImplementation(async () => {
      expect(storage.clear).toHaveBeenCalledWith('hyperliquid', ownerB)
      expect(storage.clear).toHaveBeenCalledWith('aster', ownerB)
      await disconnect()
    })
  }
  renderAccounts(config)
  const card = within(screen.getByRole('group', { name: `${venue} account` }))
  await waitFor(() => expect(card.getByRole('button', { name: 'Disconnect EVM Wallet' }).hasAttribute('disabled')).toBe(false))
  expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerB)
  fireEvent.click(card.getByRole('button', { name: 'Disconnect EVM Wallet' }))
  await waitFor(() => {
    expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe('none')
    expect(screen.getByLabelText('Aster owner').textContent).toBe('none')
    expect(screen.getByLabelText('Wagmi status').textContent).toBe('disconnected')
  })
  expect(getConnections(config)).toHaveLength(0)
  expect(storage.clear).toHaveBeenCalledWith('hyperliquid', ownerB)
  expect(storage.clear).toHaveBeenCalledWith('aster', ownerB)
})

it.each(['Hyperliquid', 'Aster'])('selecting wallet B in the %s picker removes the existing wallet A connection', async (venue) => {
  const config = walletConfig()
  renderAccounts(config)
  fireEvent.click(within(screen.getByRole('group', { name: `${venue} account` })).getByRole('button', { name: 'Connect', exact: true }))
  // A persisted/background connection becomes available while the user has the picker open.
  await act(async () => { await connect(config, { connector: config.connectors[0] }) })
  expect(getConnections(config)).toHaveLength(1)
  expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerA)
  fireEvent.click(screen.getAllByRole('button', { name: /Mock Connector/ })[1])
  await waitFor(() => {
    expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerB)
    expect(screen.getByLabelText('Aster owner').textContent).toBe(ownerB)
    expect(getConnections(config).map((connection) => connection.connector.uid)).toEqual([config.connectors[1].uid])
  })
  expect(screen.getByLabelText('Wagmi status').textContent).toBe('connected')
})

it.each(['Hyperliquid', 'Aster'])('the single-connector %s connect path also keeps only the selected wallet B', async (venue) => {
  const config = walletConfig(true)
  const connector = config.connectors[0]
  const prompt = Promise.withResolvers<void>()
  const connectWallet = connector.connect.bind(connector)
  vi.spyOn(connector, 'connect').mockImplementation(async (...args) => {
    await prompt.promise
    return connectWallet(...args)
  })
  renderAccounts(config)
  fireEvent.click(within(screen.getByRole('group', { name: `${venue} account` })).getByRole('button', { name: 'Connect', exact: true }))
  // Another session finishes restoring while B's wallet prompt is pending.
  await act(async () => { await connect(config, { connector: mock({ accounts: [ownerA] }) }) })
  expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerA)
  await act(async () => { prompt.resolve() })
  await waitFor(() => {
    expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerB)
    expect(screen.getByLabelText('Aster owner').textContent).toBe(ownerB)
    expect(getConnections(config).map((connection) => connection.connector.uid)).toEqual([connector.uid])
  })
})
