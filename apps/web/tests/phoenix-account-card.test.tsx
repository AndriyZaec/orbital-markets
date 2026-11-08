import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { ConnectAccounts } from '../src/components/ConnectAccounts'
import phoenixLogo from '../src/assets/phoenix-logo.svg'

const mocks = vi.hoisted(() => ({ disconnect: vi.fn().mockResolvedValue(undefined), refresh: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@solana/wallet-adapter-react-ui', () => ({ useWalletModal: () => ({ setVisible: vi.fn() }) }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: true, publicKey: { toBase58: () => 'SolanaOwner' }, signTransaction: vi.fn() }) }))
vi.mock('wagmi', () => ({ useConnect: () => ({ connect: vi.fn(), connectors: [] }) }))
vi.mock('@/lib/analytics', () => ({ trackAnalytics: vi.fn() }))
vi.mock('@/hooks/useLiveExecution', () => ({ useLiveExecution: () => ({ state: { phase: 'idle' } }) }))
vi.mock('@/hooks/useTelegramLink', () => ({ useTelegramLink: () => ({}) }))
vi.mock('@/hooks/useLivePositions', () => ({ useLivePositions: () => ({ positions: [{ state: 'open', venue_a: 'phoenix', venue_b: 'hyperliquid' }] }) }))
vi.mock('@/hooks/useTradingAgents', () => ({ useTradingAgents: () => ({
  pacifica: { status: 'missing' }, hyperliquid: { status: 'missing' }, aster: { status: 'missing' },
  phoenix: { status: 'ready', ownerAddress: 'SolanaOwner' }, disconnectWallet: mocks.disconnect, revokePhoenix: vi.fn(), authorize: vi.fn(),
}) }))
vi.mock('@/hooks/useVenueReadiness', () => ({ useVenueReadiness: () => {
  const disconnected = { status: 'disconnected', walletConnected: false, blockingReasons: [], agentStatus: 'missing' }
  return { pacifica: disconnected, hyperliquid: disconnected, aster: disconnected,
    phoenix: { ...disconnected, status: 'ready', walletConnected: true, signerReady: true, agentReady: true, agentStatus: 'ready',
      shortAddress: 'Sola…wner', balanceReady: true, equity: 100, available: 80 },
    aggregate: { readyCount: 1, totalCount: 4, statusLabel: 'Needs attention', tradingReady: false, blockingReasons: [] },
    ensureStatus: 'ready', refreshBalances: mocks.refresh }
} }))
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
