import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import bs58 from 'bs58'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { useVenueAccounts } from '../src/accounts/useVenueAccounts'
import { useVenueAuthority } from '../src/hooks/useVenueAuthority'
import type { Venue } from '../src/agents/types'
import { WalletSyncedVenueAccounts } from './venue-accounts-test-provider'

const wallets = vi.hoisted(() => ({ evm: null as string | null, solana: null as string | null,
  signMessage: undefined as (() => Promise<Uint8Array>) | undefined,
  signTransaction: undefined as (() => Promise<void>) | undefined,
}))
vi.mock('wagmi', () => ({ useAccount: () => ({ isConnected: !!wallets.evm, address: wallets.evm }) }))
vi.mock('@solana/wallet-adapter-react', () => ({ useWallet: () => ({ connected: !!wallets.solana,
  publicKey: wallets.solana ? { toBase58: () => wallets.solana } : null,
  signMessage: wallets.signMessage, signTransaction: wallets.signTransaction,
}) }))

const evmA = '0x1111111111111111111111111111111111111111'
const evmB = '0xAbCdEf0123456789AbCdEf0123456789AbCdEf01'
const solanaA = bs58.encode(new Uint8Array(32).fill(1))
const solanaB = bs58.encode(new Uint8Array(32).fill(2))
const empty = { pacifica: null, hyperliquid: null, aster: null, phoenix: null }
const venues: Venue[] = ['pacifica', 'hyperliquid', 'aster', 'phoenix']

beforeEach(() => { wallets.evm = null; wallets.solana = null; wallets.signMessage = undefined; wallets.signTransaction = undefined })
afterEach(cleanup)

it.each(venues)('links, replaces, and unlinks only the %s owner', (venue) => {
  const { result } = renderHook(useVenueAccounts, { wrapper: VenueAccountsProvider })
  const evm = venue === 'hyperliquid' || venue === 'aster'
  expect(result.current.accounts).toEqual(empty)
  act(() => result.current.link(venue, evm ? evmA : solanaA))
  expect(result.current.accounts).toEqual({ ...empty, [venue]: evm ? evmA : solanaA })
  act(() => result.current.link(venue, evm ? evmB : solanaB))
  expect(result.current.accounts).toEqual({ ...empty, [venue]: evm ? evmB : solanaB })
  act(() => result.current.unlink(venue))
  expect(result.current.accounts).toEqual(empty)
})

it.each(venues)('rejects invalid %s addresses without changing the linked owner', (venue) => {
  const { result } = renderHook(useVenueAccounts, { wrapper: VenueAccountsProvider })
  const evm = venue === 'hyperliquid' || venue === 'aster'
  act(() => result.current.link(venue, evm ? evmA : solanaA))
  const invalid = evm
    ? ['', solanaA, '0x1234', `0x${'g'.repeat(40)}`, `0x${'1'.repeat(41)}`, ` ${evmA}`]
    : ['', evmA, 'not-a-public-key', bs58.encode(new Uint8Array(31).fill(1)), bs58.encode(new Uint8Array(33).fill(1))]
  for (const address of invalid) {
    expect(() => result.current.link(venue, address)).toThrow(`Invalid ${venue} owner address`)
    expect(result.current.accounts).toEqual({ ...empty, [venue]: evm ? evmA : solanaA })
  }
})

it('temporarily mirrors wallet connection, account changes, and disconnection per wallet family', () => {
  const { result, rerender } = renderHook(useVenueAccounts, { wrapper: WalletSyncedVenueAccounts })
  expect(result.current.accounts).toEqual(empty)
  wallets.evm = evmA
  wallets.solana = solanaA
  rerender()
  expect(result.current.accounts).toEqual({ pacifica: solanaA, phoenix: solanaA, hyperliquid: evmA, aster: evmA })
  wallets.evm = evmB
  rerender()
  expect(result.current.accounts).toEqual({ pacifica: solanaA, phoenix: solanaA, hyperliquid: evmB, aster: evmB })
  wallets.solana = solanaB
  rerender()
  expect(result.current.accounts).toEqual({ pacifica: solanaB, phoenix: solanaB, hyperliquid: evmB, aster: evmB })
  wallets.evm = null
  rerender()
  expect(result.current.accounts).toEqual({ ...empty, pacifica: solanaB, phoenix: solanaB })
  wallets.solana = null
  rerender()
  expect(result.current.accounts).toEqual(empty)
})

it('reads independent authority addresses from the registry while preserving wallet capability checks', () => {
  wallets.evm = evmA
  wallets.solana = solanaA
  const { result, rerender } = renderHook(() => ({ registry: useVenueAccounts(), authority: useVenueAuthority() }), { wrapper: VenueAccountsProvider })
  act(() => {
    result.current.registry.link('pacifica', solanaA)
    result.current.registry.link('phoenix', solanaB)
    result.current.registry.link('hyperliquid', evmA)
    result.current.registry.link('aster', evmB)
  })
  expect(result.current.authority.pacifica).toMatchObject({ address: solanaA, readiness: 'connected_cannot_sign' })
  expect(result.current.authority.phoenix).toMatchObject({ address: solanaB, readiness: 'connected_cannot_sign' })
  expect(result.current.authority.hyperliquid).toMatchObject({ address: evmA, readiness: 'ready' })
  expect(result.current.authority.aster).toMatchObject({ address: evmB, readiness: 'ready' })
  wallets.signMessage = async () => new Uint8Array(64)
  wallets.signTransaction = async () => {}
  rerender()
  expect(result.current.authority.pacifica.readiness).toBe('ready')
  expect(result.current.authority.phoenix.readiness).toBe('ready')
  wallets.evm = null
  wallets.solana = null
  rerender()
  expect(result.current.authority.venueAuthorities.map(({ readiness }) => readiness)).toEqual(Array(4).fill('not_connected'))
  expect(result.current.registry.accounts).toEqual({ pacifica: solanaA, phoenix: solanaB, hyperliquid: evmA, aster: evmB })
  expect(result.current.authority.asterAddress).toBe(evmB)
  expect(result.current.authority.phoenixAddress).toBe(solanaB)
})
