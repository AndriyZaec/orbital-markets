import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import bs58 from 'bs58'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { useVenueAccounts } from '../src/accounts/useVenueAccounts'
import { venueAccountsKey } from '../src/accounts/venue-accounts-storage'
import { ConnectAccounts } from '../src/components/ConnectAccounts'

const mocks = vi.hoisted(() => ({ owner: null as string | null, visible: false, connecting: false, disconnect: vi.fn(), authorize: vi.fn(), revoke: vi.fn(), refresh: async () => {} }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: !!mocks.owner,
  publicKey: mocks.owner ? { toBase58: () => mocks.owner } : null, disconnect: mocks.disconnect, connecting: mocks.connecting, signTransaction: vi.fn(), signMessage: vi.fn(),
}) }))
vi.mock('@solana/wallet-adapter-react-ui', () => ({ useWalletModal: () => ({ visible: mocks.visible, setVisible: (visible: boolean) => { mocks.visible = visible } }) }))
vi.mock('wagmi', () => ({ useAccount: () => ({ isConnected: false }), useConnect: () => ({ connectors: [] }), useDisconnect: () => ({}), useConfig: () => ({}) }))
vi.mock('@/lib/analytics', () => ({ trackAnalytics: vi.fn() }))
vi.mock('@/hooks/useLiveExecution', () => ({ useLiveExecution: () => ({ state: { phase: 'idle' } }) }))
vi.mock('@/hooks/useLivePositions', () => ({ useLivePositions: () => ({ positions: [] }) }))
vi.mock('@/hooks/useTelegramLink', () => ({ useTelegramLink: () => ({}) }))
vi.mock('@/hooks/useTradingAgents', () => ({ useTradingAgents: () => {
  const { accounts } = useVenueAccounts()
  return { ...Object.fromEntries(Object.entries(accounts).map(([venue, ownerAddress]) => [venue, { ownerAddress, status: 'missing' }])), authorize: mocks.authorize, revokePhoenix: mocks.revoke }
} }))
vi.mock('@/hooks/useVenueReadiness', () => ({ useVenueReadiness: () => {
  const { accounts } = useVenueAccounts()
  return { ...Object.fromEntries(Object.entries(accounts).map(([venue, address]) => [venue, {
    venue, address, shortAddress: address, walletConnected: !!address, signerReady: address === mocks.owner,
    agentReady: false, agentStatus: 'missing', status: address ? 'agent_missing' : 'disconnected',
  }])), aggregate: { readyCount: 0, totalCount: 4, statusLabel: 'Not connected' }, refreshBalances: mocks.refresh }
} }))

const ownerA = bs58.encode(new Uint8Array(32).fill(1))
const ownerB = bs58.encode(new Uint8Array(32).fill(2))
const tree = () => <VenueAccountsProvider><ConnectAccounts open onClose={() => {}} /></VenueAccountsProvider>
const phoenix = () => within(screen.getByRole('group', { name: 'Phoenix account' }))

beforeEach(() => {
  localStorage.clear(); mocks.owner = ownerA; mocks.visible = false; mocks.connecting = false; vi.clearAllMocks()
  localStorage.setItem(venueAccountsKey, JSON.stringify({ pacifica: ownerA }))
  mocks.disconnect.mockImplementation(async () => { mocks.owner = null })
})
afterEach(cleanup)

it('offers the connected Solana wallet for Phoenix without changing Pacifica', async () => {
  render(tree())
  fireEvent.click(phoenix().getByRole('button', { name: 'Connect' }))
  fireEvent.click(await screen.findByRole('button', { name: `Use ${ownerA.slice(0, 6)}…${ownerA.slice(-4)}` }))
  await waitFor(() => expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).phoenix).toBe(ownerA))
  expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).pacifica).toBe(ownerA)
  expect(mocks.disconnect).not.toHaveBeenCalled()
  expect(mocks.authorize).not.toHaveBeenCalled()
  expect(mocks.revoke).not.toHaveBeenCalled()
})

it.each([ownerB, ownerA])('uses another Solana wallet only for Phoenix, preserving Pacifica (%s)', async (nextOwner) => {
  const view = render(tree())
  fireEvent.click(phoenix().getByRole('button', { name: 'Connect' }))
  fireEvent.click(await screen.findByRole('button', { name: 'Use another wallet' }))
  await waitFor(() => expect(mocks.disconnect).toHaveBeenCalledTimes(1))
  expect(mocks.visible).toBe(true)
  view.rerender(tree()) // Wallet adapter has disconnected and its modal is open.
  mocks.visible = false; mocks.connecting = true
  view.rerender(tree()) // Selecting the adapter closes the picker before approval.
  expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).phoenix).toBeNull()
  mocks.owner = nextOwner; mocks.connecting = false
  view.rerender(tree())
  await waitFor(() => expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).phoenix).toBe(nextOwner))
  expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).pacifica).toBe(ownerA)
  expect(mocks.authorize).not.toHaveBeenCalled()
  expect(mocks.revoke).not.toHaveBeenCalled()
  if (nextOwner === ownerA) expect(screen.getByText('Switch accounts in your wallet to use a different address.')).toBeTruthy()
})

it('links only the requested Solana venue after connecting from a disconnected wallet', async () => {
  mocks.owner = null
  const view = render(tree())
  fireEvent.click(phoenix().getByRole('button', { name: 'Connect' }))
  expect(mocks.visible).toBe(true)
  view.rerender(tree())
  mocks.visible = false; mocks.connecting = true
  view.rerender(tree())
  mocks.owner = ownerB; mocks.connecting = false
  view.rerender(tree())
  await waitFor(() => expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).phoenix).toBe(ownerB))
  expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).pacifica).toBe(ownerA)
})

it('does not link a later background connection after the picker was dismissed', () => {
  mocks.owner = null
  const view = render(tree())
  fireEvent.click(phoenix().getByRole('button', { name: 'Connect' }))
  view.rerender(tree())
  mocks.visible = false
  view.rerender(tree())
  mocks.owner = ownerB
  view.rerender(tree())
  expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).phoenix ?? null).toBeNull()
})

it('reconnects bound Pacifica without replacing its owner when a different Solana account connects', async () => {
  mocks.owner = ownerB
  const view = render(tree())
  const pacifica = within(screen.getByRole('group', { name: 'Pacifica account' }))
  fireEvent.click(pacifica.getByRole('button', { name: 'Connect', exact: true }))
  await waitFor(() => expect(mocks.disconnect).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole('button', { name: 'Use another wallet' })).toBeNull()
  view.rerender(tree())
  mocks.visible = false; mocks.connecting = true
  view.rerender(tree())
  mocks.owner = ownerB; mocks.connecting = false
  view.rerender(tree())
  await screen.findByText(`Switch to ${ownerA.slice(0, 6)}…${ownerA.slice(-4)} in your wallet`)
  expect(JSON.parse(localStorage.getItem(venueAccountsKey)!).pacifica).toBe(ownerA)
  expect(mocks.authorize).not.toHaveBeenCalled()
  expect(mocks.revoke).not.toHaveBeenCalled()
})
