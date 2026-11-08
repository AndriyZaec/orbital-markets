import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ConnectAccounts } from '../src/components/ConnectAccounts'
import phoenixLogo from '../src/assets/phoenix-logo.svg'

const mocks = vi.hoisted(() => ({ disconnect: vi.fn().mockResolvedValue(undefined), refresh: vi.fn().mockResolvedValue(undefined),
  revoke: vi.fn().mockResolvedValue(undefined), positions: [{ state: 'open', venue_a: 'phoenix', venue_b: 'hyperliquid' }],
}))
vi.mock('@solana/wallet-adapter-react-ui', () => ({ useWalletModal: () => ({ setVisible: vi.fn() }) }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: true, publicKey: { toBase58: () => 'SolanaOwner' }, signTransaction: vi.fn() }) }))
vi.mock('wagmi', () => ({ useConnect: () => ({ connect: vi.fn(), connectors: [] }) }))
vi.mock('@/lib/analytics', () => ({ trackAnalytics: vi.fn() }))
vi.mock('@/hooks/useLiveExecution', () => ({ useLiveExecution: () => ({ state: { phase: 'idle' } }) }))
vi.mock('@/hooks/useTelegramLink', () => ({ useTelegramLink: () => ({}) }))
vi.mock('@/hooks/useLivePositions', () => ({ useLivePositions: () => ({ positions: mocks.positions }) }))
vi.mock('@/hooks/useTradingAgents', () => ({ useTradingAgents: () => ({
  pacifica: { status: 'missing' }, hyperliquid: { status: 'missing' }, aster: { status: 'missing' },
  phoenix: { status: 'ready', ownerAddress: 'SolanaOwner' }, disconnectWallet: mocks.disconnect, revokePhoenix: mocks.revoke, authorize: vi.fn(),
}) }))
vi.mock('@/hooks/useVenueReadiness', () => ({ useVenueReadiness: () => {
  const disconnected = { status: 'disconnected', walletConnected: false, blockingReasons: [], agentStatus: 'missing' }
  return { pacifica: disconnected, hyperliquid: disconnected, aster: disconnected,
    phoenix: { ...disconnected, status: 'ready', walletConnected: true, signerReady: true, agentReady: true, agentStatus: 'ready',
      shortAddress: 'Sola…wner', balanceReady: true, equity: 100, available: 80 },
    aggregate: { readyCount: 1, totalCount: 4, statusLabel: 'Needs attention', tradingReady: false, blockingReasons: [] },
    ensureStatus: 'ready', refreshBalances: mocks.refresh }
} }))
beforeEach(() => {
  vi.clearAllMocks()
  mocks.positions = [{ state: 'open', venue_a: 'phoenix', venue_b: 'hyperliquid' }]
})
afterEach(cleanup)

it('uses the shared branded account card and confirms Solana exposure before disconnecting Phoenix', async () => {
  render(<ConnectAccounts open onClose={() => {}} />)
  const card = within(screen.getByRole('group', { name: 'Phoenix account' }))
  expect(card.getByRole('img', { name: 'Phoenix' }).getAttribute('src')).toBe(phoenixLogo)
  expect(card.getByText('Owner signer')).toBeTruthy()
  expect(card.getByText('Equity / Available')).toBeTruthy()
  expect(screen.getAllByRole('group', { name: 'Phoenix account' })).toHaveLength(1)
  fireEvent.click(card.getByRole('button', { name: 'Disconnect Solana' }))
  const confirmation = await screen.findByRole('dialog')
  expect(mocks.disconnect).not.toHaveBeenCalled()
  fireEvent.click(within(confirmation).getByRole('button', { name: 'Disconnect', exact: true }))
  await waitFor(() => expect(mocks.disconnect).toHaveBeenCalledWith('solana'))
})

it('warns before revoking Phoenix exposure, cancels cleanly and only revokes on confirmation', async () => {
  render(<ConnectAccounts open onClose={() => {}} />)
  const card = within(screen.getByRole('group', { name: 'Phoenix account' }))
  fireEvent.click(card.getByRole('button', { name: 'Revoke Phoenix' }))
  const confirmation = await screen.findByRole('dialog', { name: 'Revoke Phoenix authorization?' })
  expect(within(confirmation).getByText(/Positions remain open/)).toBeTruthy()
  expect(mocks.revoke).not.toHaveBeenCalled()
  fireEvent.click(within(confirmation).getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(mocks.revoke).not.toHaveBeenCalled()

  fireEvent.click(card.getByRole('button', { name: 'Revoke Phoenix' }))
  const confirmed = await screen.findByRole('dialog', { name: 'Revoke Phoenix authorization?' })
  fireEvent.click(within(confirmed).getByRole('button', { name: 'Revoke authorization' }))
  await waitFor(() => expect(mocks.revoke).toHaveBeenCalledTimes(1))
  expect(mocks.disconnect).not.toHaveBeenCalled()
})

it.each(['closed', 'failed'])('does not warn for a %s Phoenix position', async (state) => {
  mocks.positions = [{ state, venue_a: 'phoenix', venue_b: 'hyperliquid' }]
  render(<ConnectAccounts open onClose={() => {}} />)
  fireEvent.click(within(screen.getByRole('group', { name: 'Phoenix account' })).getByRole('button', { name: 'Revoke Phoenix' }))
  await waitFor(() => expect(mocks.revoke).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole('dialog')).toBeNull()
})

it('does not warn when active exposure is only on other venues', async () => {
  mocks.positions = [{ state: 'open', venue_a: 'pacifica', venue_b: 'hyperliquid' }]
  render(<ConnectAccounts open onClose={() => {}} />)
  fireEvent.click(within(screen.getByRole('group', { name: 'Phoenix account' })).getByRole('button', { name: 'Revoke Phoenix' }))
  await waitFor(() => expect(mocks.revoke).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole('dialog')).toBeNull()
})
