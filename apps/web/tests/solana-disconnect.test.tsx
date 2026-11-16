import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import bs58 from 'bs58'
import { TradingAgentProvider } from '../src/agents/TradingAgentProvider'
import { PhoenixAuthorization } from '../src/components/PhoenixAuthorization'
import { useTradingAgents } from '../src/hooks/useTradingAgents'
import { apiError } from '../src/lib/api'
import { TestTradingAgentStore } from './trading-agent-test-store'
import type { PhoenixResult } from '../src/agents/phoenix-agent'
import type { StoredTradingAgent } from '../src/agents/types'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { useVenueAccounts } from '../src/accounts/useVenueAccounts'
import { venueAccountsKey } from '../src/accounts/venue-accounts-storage'

const mocks = vi.hoisted(() => ({
  connected: true, disconnect: vi.fn(), connect: vi.fn(),
  prepare: vi.fn(), reconcile: vi.fn(), signDelegation: vi.fn(),
}))
const owner = bs58.encode(new Uint8Array(32).fill(1))
const storage = new TestTradingAgentStore()
const phoenixAgent: StoredTradingAgent = {
  version: 2, venue: 'phoenix', ownerAddress: owner,
  agentAddress: bs58.encode(new Uint8Array(32).fill(3)),
  privateKey: bs58.encode(new Uint8Array(64).fill(3)), authorizedAt: new Date().toISOString(),
}
const pacificaAgent: StoredTradingAgent = { ...phoenixAgent, venue: 'pacifica', agentAddress: bs58.encode(new Uint8Array(32).fill(2)) }
const notice = 'Phoenix authorization is still active. Reconnect this wallet to revoke it.'

vi.mock('@/agents/storage', () => ({ createTradingAgentStore: () => storage }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({
  connected: mocks.connected, publicKey: { toBase58: () => owner },
  disconnect: mocks.disconnect, signMessage: async () => new Uint8Array(64),
  signTransaction: vi.fn(),
}) }))
vi.mock('wagmi', () => ({ useAccount: () => ({ isConnected: false }), useConnections: () => [],
  useDisconnect: () => ({}), useSignTypedData: () => ({}), useSwitchChain: () => ({}),
}))
// Signing bytes are covered by the Node Phoenix contract tests; isolate that
// browser-wallet boundary here while exercising the real revoke/store lifecycle.
vi.mock('@/agents/phoenix-message', () => ({ signPhoenixDelegation: mocks.signDelegation }))
vi.mock('@/agents/phoenix-agent', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../src/agents/phoenix-agent')>()
  return { ...actual,
    createPhoenixTransport: () => ({ prepare: mocks.prepare, reconcile: mocks.reconcile }),
    revokePhoenixAgent: (options: Parameters<typeof actual.revokePhoenixAgent>[0]) => actual.revokePhoenixAgent({ ...options, wait: async () => {} }),
    restorePhoenixAgent: (options: Parameters<typeof actual.restorePhoenixAgent>[0]) => actual.restorePhoenixAgent({ ...options, wait: async () => {} }),
  }
})

function Actions() {
  const agents = useTradingAgents()
  const { link, accounts } = useVenueAccounts()
  return <>
    <PhoenixAuthorization key={agents.phoenix.ownerAddress ?? 'no-owner'} disabled={false} hasActiveExposure={false}
      onConnect={() => { mocks.connect(); link('phoenix', owner) }} onDisconnect={() => void agents.disconnectVenue('phoenix').catch(() => {})} />
    <button onClick={() => void agents.disconnectVenue('pacifica').catch(() => {})}>Disconnect Pacifica</button>
    <output aria-label="Pacifica owner">{accounts.pacifica ?? 'none'}</output>
    <output aria-label="Phoenix agent">{agents.phoenix.agentAddress ?? 'none'}</output>
    <output aria-label="Phoenix status">{agents.phoenix.status}</output>
    <output aria-label="Pacifica status">{agents.pacifica.status}</output>
  </>
}

function renderSession() {
  const view = render(<TradingAgentProvider><Actions /></TradingAgentProvider>, { wrapper: VenueAccountsProvider })
  const setConnected = (connected: boolean) => {
    mocks.connected = connected
    view.rerender(<TradingAgentProvider><Actions /></TradingAgentProvider>)
  }
  mocks.disconnect.mockImplementation(async () => { setConnected(false) })
  mocks.connect.mockImplementation(() => { setConnected(true) })
  return view
}

function revokeOperation(): StoredTradingAgent {
  return { ...phoenixAgent, phoenixOperation: { requestID: 'revoke-1', action: 'revoke', targetAuthority: owner, submissionStarted: true } }
}

function revokeResult(state: PhoenixResult['state']): PhoenixResult {
  return { request_id: 'revoke-1', owner, action: 'revoke', target_authority: owner, state }
}

beforeEach(async () => {
  localStorage.clear()
  localStorage.setItem(venueAccountsKey, JSON.stringify({ pacifica: owner, phoenix: owner }))
  vi.clearAllMocks()
  mocks.connected = true
  storage.values.clear()
  storage.pendingValues.clear()
  await storage.save(phoenixAgent)
  await storage.save(pacificaAgent)
  mocks.prepare.mockReset().mockRejectedValue(apiError(503, 'Phoenix helper unavailable'))
  mocks.reconcile.mockReset()
  mocks.signDelegation.mockReset().mockRejectedValue(Object.assign(new Error('Wallet request cancelled'), { code: 4001 }))
  vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })))
})
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it.each(['helper unavailable', 'wallet rejected'])('unlinks only Phoenix after %s, retains its key, and restores it on reconnect', async (failure) => {
  renderSession()
  await waitFor(() => expect(screen.getByLabelText('Phoenix agent').textContent).toBe(phoenixAgent.agentAddress))
  if (failure === 'wallet rejected') {
    mocks.prepare.mockResolvedValue({ ...revokeResult('prepared'), signing_request: { id: 'revoke-1' } })
  }
  fireEvent.click(screen.getByRole('button', { name: 'Disconnect', exact: true }))
  await screen.findByRole('button', { name: 'Connect', exact: true })
  expect(mocks.disconnect).not.toHaveBeenCalled()
  expect(fetch).not.toHaveBeenCalled()
  expect(await storage.restore('pacifica', owner)).not.toBeNull()
  expect(screen.getByLabelText('Pacifica owner').textContent).toBe(owner)
  expect(await storage.loadForSigning('phoenix', owner)).toMatchObject(phoenixAgent)
  expect(screen.getAllByText(notice)).toHaveLength(1)
  expect(screen.getByText(notice).classList.contains('text-muted-foreground')).toBe(true)
  expect(screen.queryByRole('alert')).toBeNull()

  // The unsigned request may expire while disconnected; the same key restores.
  mocks.reconcile.mockResolvedValue(revokeResult('expired'))
  fireEvent.click(screen.getByRole('button', { name: 'Connect', exact: true }))
  await waitFor(() => {
    expect(screen.getByLabelText('Phoenix agent').textContent).toBe(phoenixAgent.agentAddress)
    expect(screen.getByLabelText('Phoenix status').textContent).toBe('ready')
    expect(screen.queryByText(notice)).toBeNull()
  })
  expect(screen.getByRole('button', { name: 'Revoke', exact: true })).toBeTruthy()
})

it.each(['pending', 'unknown'])('disconnects after a %s Phoenix revoke and preserves operation metadata and pending keys', async (outcome) => {
  renderSession()
  await waitFor(() => expect(screen.getByLabelText('Phoenix status').textContent).toBe('ready'))
  const operation = revokeOperation()
  await storage.save(operation)
  await storage.savePending({ ...operation, agentAddress: bs58.encode(new Uint8Array(32).fill(4)) })
  if (outcome === 'pending') mocks.reconcile.mockResolvedValue(revokeResult('pending'))
  else mocks.reconcile.mockRejectedValue(new TypeError('Failed to fetch'))
  fireEvent.click(screen.getByRole('button', { name: 'Disconnect', exact: true }))
  await screen.findByRole('button', { name: 'Connect', exact: true })
  expect(mocks.disconnect).not.toHaveBeenCalled()
  expect(await storage.loadForSigning('phoenix', owner)).toEqual(operation)
  expect((await storage.loadPendingForSigning('phoenix', owner))?.phoenixOperation).toEqual(operation.phoenixOperation)
  expect(await storage.restore('pacifica', owner)).not.toBeNull()
  expect(screen.getAllByText(notice)).toHaveLength(1)
  expect(screen.queryByRole('alert')).toBeNull()
})

it('clears Phoenix keys after confirmed revoke without touching Pacifica or disconnecting its wallet', async () => {
  renderSession()
  await waitFor(() => expect(screen.getByLabelText('Phoenix status').textContent).toBe('ready'))
  await storage.save(revokeOperation())
  mocks.reconcile.mockResolvedValue(revokeResult('confirmed'))
  fireEvent.click(screen.getByRole('button', { name: 'Disconnect', exact: true }))
  await screen.findByRole('button', { name: 'Connect', exact: true })
  expect(mocks.disconnect).not.toHaveBeenCalled()
  expect(await storage.loadForSigning('phoenix', owner)).toBeNull()
  expect(await storage.loadPendingForSigning('phoenix', owner)).toBeNull()
  expect(await storage.restore('pacifica', owner)).not.toBeNull()
  expect(screen.queryByText(notice)).toBeNull()
  expect(screen.queryByRole('alert')).toBeNull()
})

it('preserves existing Pacifica failure behavior and does not touch Phoenix', async () => {
  vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ error: 'Pacifica revoke rejected' }), { status: 502 }))
  renderSession()
  await waitFor(() => expect(screen.getByLabelText('Phoenix status').textContent).toBe('ready'))
  fireEvent.click(screen.getByRole('button', { name: 'Disconnect Pacifica', exact: true }))
  await waitFor(() => expect(screen.getByLabelText('Pacifica status').textContent).toBe('error'))
  expect(mocks.disconnect).not.toHaveBeenCalled()
  expect(await storage.loadForSigning('pacifica', owner)).toEqual(pacificaAgent)
  expect(await storage.loadForSigning('phoenix', owner)).toEqual(phoenixAgent)
  expect(screen.queryByText(notice)).toBeNull()
  expect(mocks.prepare).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Pacifica owner').textContent).toBe(owner)
})

it('disconnects Pacifica independently, then detaches Solana only after the last venue is unlinked', async () => {
  renderSession()
  await waitFor(() => expect(screen.getByLabelText('Phoenix status').textContent).toBe('ready'))
  fireEvent.click(screen.getByRole('button', { name: 'Disconnect Pacifica', exact: true }))
  await waitFor(() => expect(screen.getByLabelText('Pacifica owner').textContent).toBe('none'))
  expect(await storage.restore('pacifica', owner)).toBeNull()
  expect(await storage.restore('phoenix', owner)).not.toBeNull()
  expect(screen.getByLabelText('Phoenix status').textContent).toBe('ready')
  expect(mocks.disconnect).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Disconnect', exact: true }))
  await screen.findByRole('button', { name: 'Connect', exact: true })
  expect(mocks.disconnect).toHaveBeenCalledTimes(1)
  expect(screen.getAllByText(notice)).toHaveLength(1)
})

it('allows Phoenix Disconnect without the owner wallet, preserving its key and Pacifica binding', async () => {
  mocks.connected = false
  renderSession()
  await waitFor(() => expect(screen.getByLabelText('Phoenix status').textContent).toBe('ready'))
  expect(screen.getByRole('button', { name: 'Revoke', exact: true }).hasAttribute('disabled')).toBe(true)
  const disconnect = screen.getByRole('button', { name: 'Disconnect', exact: true })
  expect(disconnect.hasAttribute('disabled')).toBe(false)
  fireEvent.click(disconnect)
  await screen.findByText(notice)
  expect(await storage.loadForSigning('phoenix', owner)).toMatchObject(phoenixAgent)
  expect(screen.getByLabelText('Pacifica owner').textContent).toBe(owner)
  expect(mocks.disconnect).not.toHaveBeenCalled()
})
