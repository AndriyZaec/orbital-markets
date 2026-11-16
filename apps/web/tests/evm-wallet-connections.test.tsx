import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createConfig, http, useAccount, WagmiProvider, type Config } from 'wagmi'
import { connect, getConnections } from 'wagmi/actions'
import { mainnet } from 'wagmi/chains'
import { mock } from 'wagmi/connectors'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { venueAccountsKey } from '../src/accounts/venue-accounts-storage'
import { TradingAgentProvider } from '../src/agents/TradingAgentProvider'
import { ConnectAccounts } from '../src/components/ConnectAccounts'
import { useTradingAgents } from '../src/hooks/useTradingAgents'
import { VenueReadinessProvider } from '../src/hooks/useVenueReadiness'
import { LivePositionsProvider } from '../src/hooks/useLivePositions'
import { TestTradingAgentStore } from './trading-agent-test-store'

const storage = new TestTradingAgentStore()
const mocks = vi.hoisted(() => ({ refetch: vi.fn(async () => {}), positions: [] as Array<{ state: string; venue_a: string; venue_b: string }>, phase: 'idle' }))
vi.mock('@/agents/storage', () => ({ createTradingAgentStore: () => storage }))
vi.mock('@/agents/builder-config', () => ({ asterBuilderAddress: null, hyperliquidBuilderAddress: null }))
vi.mock('@/agents/aster-agent', () => ({ reconcilePendingAsterAgent: async () => null }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: false, publicKey: null }) }))
vi.mock('@solana/wallet-adapter-react-ui', () => ({ useWalletModal: () => ({ setVisible: vi.fn(), visible: false }) }))
vi.mock('@/lib/analytics', () => ({ trackAnalytics: vi.fn() }))
vi.mock('@/hooks/useLiveExecution', () => ({ useLiveExecution: () => ({ state: { phase: mocks.phase } }) }))
vi.mock('@/lib/live-events', async (original) => ({ ...await original<typeof import('../src/lib/live-events')>(), subscribeLiveAccountEvents: () => () => {} }))
vi.mock('@/hooks/useTelegramLink', () => ({ useTelegramLink: () => ({}) }))
vi.mock('@/hooks/useLiveBalances', () => ({ useLiveBalances: () => {
  const balance = { connected: true, stream_ready: true, fresh: true, equity: 100, available: 80 }
  return { pacifica: balance, hyperliquid: balance, aster: balance, phoenix: balance, refetch: mocks.refetch }
} }))

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
  const tree = () => <WagmiProvider config={config} reconnectOnMount={false}>
    <QueryClientProvider client={queryClient}><VenueAccountsProvider><TradingAgentProvider>
      <VenueReadinessProvider><LivePositionsProvider><WalletState /><ConnectAccounts open onClose={() => {}} /></LivePositionsProvider></VenueReadinessProvider>
    </TradingAgentProvider></VenueAccountsProvider></QueryClientProvider>
  </WagmiProvider>
  const view = render(tree())
  return { ...view, rerenderAccounts: () => view.rerender(tree()) }
}

async function seed(bindings: { hyperliquid?: string; aster?: string }) {
  localStorage.setItem(venueAccountsKey, JSON.stringify(bindings))
  for (const venue of ['hyperliquid', 'aster'] as const) {
    const ownerAddress = bindings[venue]
    if (ownerAddress) await storage.save({ version: 2, venue, ownerAddress, agentAddress: `${venue}-agent`, privateKey: 'test-key', authorizedAt: '2026-01-01' })
  }
}

beforeEach(() => {
  localStorage.clear(); storage.values.clear(); mocks.positions = []; mocks.phase = 'idle'
  vi.stubGlobal('fetch', vi.fn(async (url: string) => Response.json(url.includes('/live/positions') ? mocks.positions : {})))
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('disconnects Aster alone, preserving Hyperliquid, its agent, and the connected wallet', async () => {
  await seed({ hyperliquid: ownerA, aster: ownerA })
  const config = walletConfig()
  await connect(config, { connector: config.connectors[0] })
  renderAccounts(config)
  const aster = within(screen.getByRole('group', { name: 'Aster account' }))
  await waitFor(() => expect(aster.getByRole('button', { name: 'Disconnect' }).hasAttribute('disabled')).toBe(false))
  fireEvent.click(aster.getByRole('button', { name: 'Disconnect' }))
  await waitFor(() => expect(screen.getByLabelText('Aster owner').textContent).toBe('none'))
  expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerA)
  expect(screen.getByLabelText('Wagmi status').textContent).toBe('connected')
  expect(await storage.restore('hyperliquid', ownerA)).not.toBeNull()
  expect(await storage.restore('aster', ownerA)).toBeNull()
  expect(getConnections(config)).toHaveLength(1)
})

it.each(['hyperliquid', 'aster'] as const)('disconnecting the last EVM venue (%s) disconnects all wallet connections', async (venue) => {
  await seed({ [venue]: ownerB })
  const config = walletConfig()
  await connect(config, { connector: config.connectors[0] })
  await connect(config, { connector: config.connectors[1] })
  renderAccounts(config)
  const card = within(screen.getByRole('group', { name: `${venue === 'aster' ? 'Aster' : 'Hyperliquid'} account` }))
  await waitFor(() => expect(card.getByRole('button', { name: 'Disconnect' }).hasAttribute('disabled')).toBe(false))
  fireEvent.click(card.getByRole('button', { name: 'Disconnect' }))
  await waitFor(() => expect(screen.getByLabelText('Wagmi status').textContent).toBe('disconnected'))
  expect(getConnections(config)).toHaveLength(0)
  expect(await storage.restore(venue, ownerB)).toBeNull()
})

it('Use connected links only Aster to the existing wallet address', async () => {
  await seed({ hyperliquid: ownerA })
  const config = walletConfig()
  await connect(config, { connector: config.connectors[0] })
  renderAccounts(config)
  fireEvent.click(within(screen.getByRole('group', { name: 'Aster account' })).getByRole('button', { name: 'Connect', exact: true }))
  fireEvent.click(await screen.findByRole('button', { name: 'Use 0x1111…1111' }))
  await waitFor(() => expect(screen.getByLabelText('Aster owner').textContent).toBe(ownerA))
  expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerA)
  expect(await storage.restore('hyperliquid', ownerA)).not.toBeNull()
})

it.each([false, true])('Use another wallet links Aster B and preserves Hyperliquid A (single connector: %s)', async (single) => {
  await seed({ hyperliquid: ownerA })
  const config = walletConfig(single)
  await connect(config, { connector: single ? mock({ accounts: [ownerA] }) : config.connectors[0] })
  renderAccounts(config)
  fireEvent.click(within(screen.getByRole('group', { name: 'Aster account' })).getByRole('button', { name: 'Connect', exact: true }))
  fireEvent.click(await screen.findByRole('button', { name: 'Use another wallet' }))
  if (!single) fireEvent.click((await screen.findAllByRole('button', { name: /Mock Connector/ }))[1])
  await waitFor(() => expect(screen.getByLabelText('Aster owner').textContent).toBe(ownerB))
  expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerA)
  expect(await storage.restore('hyperliquid', ownerA)).not.toBeNull()
  expect(getConnections(config)).toHaveLength(1)
  // The real readiness ensure request carries both independently linked accounts.
  await waitFor(() => expect(vi.mocked(fetch).mock.calls.some(([, init]) => {
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null
    return body?.accounts?.aster === ownerB && body?.accounts?.hyperliquid === ownerA
  })).toBe(true))
})

it('still links the same address after Use another wallet and gives a short account-switch hint', async () => {
  await seed({ hyperliquid: ownerA })
  const config = walletConfig()
  await connect(config, { connector: config.connectors[0] })
  renderAccounts(config)
  fireEvent.click(within(screen.getByRole('group', { name: 'Aster account' })).getByRole('button', { name: 'Connect', exact: true }))
  fireEvent.click(await screen.findByRole('button', { name: 'Use another wallet' }))
  fireEvent.click((await screen.findAllByRole('button', { name: /Mock Connector/ }))[0])
  await screen.findByText('Switch accounts in your wallet to use a different address.')
  await waitFor(() => expect(screen.getByLabelText('Aster owner').textContent).toBe(ownerA))
})

it('keeps a linked agent ready while its owner wallet is disconnected', async () => {
  await seed({ hyperliquid: ownerA, aster: ownerB })
  renderAccounts(walletConfig())
  const card = within(screen.getByRole('group', { name: 'Aster account' }))
  await waitFor(() => expect(card.getByRole('button', { name: 'Reauthorize' }).hasAttribute('disabled')).toBe(true))
  expect(card.getByText('Connected')).toBeTruthy()
  expect(card.getByText('Missing')).toBeTruthy() // Owner signer only.
  expect(card.getByText('Ready')).toBeTruthy() // Agent authorization.
  expect(card.getByRole('button', { name: 'Disconnect' }).hasAttribute('disabled')).toBe(false)
  expect(screen.getByText('2/4 active venues ready')).toBeTruthy()
})

it('does not confirm Aster disconnect for exposure on Hyperliquid only, and keeps live-execution blocking', async () => {
  await seed({ hyperliquid: ownerA, aster: ownerA })
  mocks.positions = [{ state: 'open', venue_a: 'hyperliquid', venue_b: 'pacifica' }]
  const config = walletConfig()
  await connect(config, { connector: config.connectors[0] })
  const view = renderAccounts(config)
  const card = within(screen.getByRole('group', { name: 'Aster account' }))
  await waitFor(() => expect(card.getByRole('button', { name: 'Disconnect' }).hasAttribute('disabled')).toBe(false))
  mocks.phase = 'submitting'
  await act(async () => view.rerenderAccounts())
  expect(card.getByRole('button', { name: 'Disconnect' }).hasAttribute('disabled')).toBe(true)
  mocks.phase = 'idle'
  await act(async () => view.rerenderAccounts())
  fireEvent.click(card.getByRole('button', { name: 'Disconnect' }))
  await waitFor(() => expect(screen.getByLabelText('Aster owner').textContent).toBe('none'))
  expect(screen.queryByRole('dialog')).toBeNull()
})

it('reconnects a bound Hyperliquid owner without relinking it or changing Aster and keeps position queries bound to A', async () => {
  await seed({ hyperliquid: ownerA, aster: ownerB })
  const config = walletConfig()
  await connect(config, { connector: config.connectors[0] })
  await connect(config, { connector: config.connectors[1] })
  renderAccounts(config)
  const hl = within(screen.getByRole('group', { name: 'Hyperliquid account' }))
  await waitFor(() => expect(hl.getByRole('button', { name: 'Connect', exact: true }).hasAttribute('disabled')).toBe(false))
  fireEvent.click(hl.getByRole('button', { name: 'Connect', exact: true }))
  const picker = await screen.findAllByRole('button', { name: /Mock Connector/ })
  expect(screen.queryByRole('button', { name: 'Use another wallet' })).toBeNull()
  expect(getConnections(config).map(({ connector }) => connector.uid)).toEqual([config.connectors[0].uid])
  fireEvent.click(picker[1]) // A different account connects again; never relink HL.
  await screen.findByText('Switch to 0x1111…1111 in your wallet')
  expect(screen.getByLabelText('Hyperliquid owner').textContent).toBe(ownerA)
  expect(screen.getByLabelText('Aster owner').textContent).toBe(ownerB)
  expect(await storage.restore('hyperliquid', ownerA)).not.toBeNull()
  expect(await storage.restore('aster', ownerB)).not.toBeNull()
  const positionCalls = vi.mocked(fetch).mock.calls.filter(([url]) => String(url).includes('/live/positions'))
  expect(positionCalls.length).toBeGreaterThan(0)
  for (const [url] of positionCalls) {
    const query = new URL(String(url), 'https://orbital.test').searchParams
    expect(query.get('accounts[hyperliquid]')).toBe(ownerA)
    expect(query.get('accounts[aster]')).toBe(ownerB)
  }
  fireEvent.click(hl.getByRole('button', { name: 'Connect', exact: true }))
  fireEvent.click((await screen.findAllByRole('button', { name: /Mock Connector/ }))[0])
  await waitFor(() => expect(hl.queryByRole('button', { name: 'Connect', exact: true })).toBeNull())
  expect(hl.getByRole('button', { name: 'Reauthorize' }).hasAttribute('disabled')).toBe(false)
  expect(screen.getByLabelText('Aster owner').textContent).toBe(ownerB)
})
