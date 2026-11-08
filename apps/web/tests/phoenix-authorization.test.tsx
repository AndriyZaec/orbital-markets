import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { PhoenixAuthorization } from '../src/components/PhoenixAuthorization'

const mocks = vi.hoisted(() => ({
  authorize: vi.fn(), revoke: vi.fn(),
  owner: 'owner-A', status: 'missing',
}))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: true, publicKey: { toBase58: () => mocks.owner }, signTransaction: vi.fn() }) }))
vi.mock('@/hooks/useTradingAgents', () => ({ useTradingAgents: () => ({
  phoenix: { venue: 'phoenix', status: mocks.status, ownerAddress: mocks.owner, agentAddress: null, error: null },
  authorize: mocks.authorize, revokePhoenix: mocks.revoke,
}) }))
vi.mock('@/hooks/useVenueReadiness', () => ({ useVenueReadiness: () => ({ phoenix: { status: 'unavailable', blockingReasons: ['Phoenix infrastructure not ready'] } }) }))

beforeEach(() => { mocks.authorize.mockReset().mockResolvedValue(undefined); mocks.revoke.mockReset().mockResolvedValue(undefined); mocks.owner = 'owner-A'; mocks.status = 'missing' })
afterEach(cleanup)

it('presents Phoenix execution authorization and requests explicit delegate replacement', async () => {
  mocks.authorize.mockRejectedValueOnce(Object.assign(new Error('replacement required'), { name: 'PhoenixReplacementRequired', authority: 'existing-delegate' }))
  render(<PhoenixAuthorization disabled={false} onConnect={() => {}} />)
  expect(screen.getByText(/Trade using your existing Phoenix cross account\./)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Authorize Phoenix' }))
  await screen.findByRole('dialog')
  expect(screen.getByText('existing-delegate')).toBeTruthy()
  expect(mocks.authorize).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('button', { name: 'Replace delegate' }))
  await waitFor(() => expect(mocks.authorize).toHaveBeenCalledTimes(2))
  expect(mocks.authorize).toHaveBeenLastCalledWith('phoenix', { replaceExistingDelegate: true, expectedOwner: 'owner-A', expectedAuthority: 'existing-delegate' })
})

it('does not reuse a replacement confirmation after the connected owner changes', async () => {
  mocks.authorize.mockRejectedValueOnce(Object.assign(new Error('replacement required'), { name: 'PhoenixReplacementRequired', authority: 'existing-delegate' }))
  const view = render(<PhoenixAuthorization disabled={false} onConnect={() => {}} />)
  fireEvent.click(screen.getByRole('button', { name: 'Authorize Phoenix' }))
  await screen.findByRole('dialog')
  mocks.owner = 'owner-B'
  view.rerender(<PhoenixAuthorization disabled={false} onConnect={() => {}} />)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(mocks.authorize).toHaveBeenCalledTimes(1)
})
