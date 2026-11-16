import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import bs58 from 'bs58'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { useVenueAccounts } from '../src/accounts/useVenueAccounts'
import { useVenueAuthority } from '../src/hooks/useVenueAuthority'
import type { Venue } from '../src/agents/types'
import { MigrateVenueAccounts } from '../src/accounts/MigrateVenueAccounts'
import { venueAccountsKey } from '../src/accounts/venue-accounts-storage'
import type { ReactNode } from 'react'

const wallets = vi.hoisted(() => ({ evm: null as string | null, solana: null as string | null,
  restore: vi.fn(),
  signMessage: undefined as (() => Promise<Uint8Array>) | undefined,
  signTransaction: undefined as (() => Promise<void>) | undefined,
}))
vi.mock('@/agents/storage', () => ({ createTradingAgentStore: () => ({ restore: wallets.restore }) }))
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

beforeEach(() => { localStorage.clear(); wallets.restore.mockReset().mockResolvedValue(null); wallets.evm = null; wallets.solana = null; wallets.signMessage = undefined; wallets.signTransaction = undefined })
afterEach(() => { cleanup(); vi.restoreAllMocks() })

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

function MigrationProvider({ children }: { children: ReactNode }) {
  return <VenueAccountsProvider><MigrateVenueAccounts />{children}</VenueAccountsProvider>
}

it('migrates only saved agents once, then ignores wallet changes and disconnections', async () => {
  wallets.evm = evmA; wallets.solana = solanaA
  wallets.restore.mockImplementation(async (venue: string) => venue === 'aster' || venue === 'pacifica' ? { agentAddress: 'saved-agent' } : null)
  const { result, rerender } = renderHook(useVenueAccounts, { wrapper: MigrationProvider })
  await waitFor(() => expect(result.current.accounts).toEqual({ ...empty, pacifica: solanaA, aster: evmA }))
  expect(JSON.parse(localStorage.getItem(venueAccountsKey)!)).toEqual(result.current.accounts)
  wallets.evm = evmB
  rerender()
  wallets.evm = null
  wallets.solana = null
  rerender()
  expect(result.current.accounts).toEqual({ ...empty, pacifica: solanaA, aster: evmA })
  expect(wallets.restore).toHaveBeenCalledTimes(4)
})

it('writes an empty migration result and does not import again on reload', async () => {
  wallets.evm = evmA
  const first = renderHook(useVenueAccounts, { wrapper: MigrationProvider })
  await waitFor(() => expect(localStorage.getItem(venueAccountsKey)).not.toBeNull())
  expect(first.result.current.accounts).toEqual(empty)
  first.unmount()
  wallets.restore.mockClear().mockResolvedValue({ agentAddress: 'later-agent' })
  const { result } = renderHook(useVenueAccounts, { wrapper: MigrationProvider })
  expect(result.current.accounts).toEqual(empty)
  expect(wallets.restore).not.toHaveBeenCalled()
})

it('restores valid saved entries synchronously on the first render, discards invalid entries, and persists unlink', () => {
  localStorage.setItem(venueAccountsKey, JSON.stringify({ aster: evmB, hyperliquid: '0xBAD', pacifica: solanaA, phoenix: 'invalid', unknown: evmA }))
  const seen: unknown[] = []
  const first = renderHook(() => { const registry = useVenueAccounts(); seen.push(registry.accounts); return registry }, { wrapper: VenueAccountsProvider })
  expect(seen[0]).toEqual({ ...empty, aster: evmB, pacifica: solanaA })
  act(() => first.result.current.unlink('aster'))
  first.unmount()
  const second = renderHook(useVenueAccounts, { wrapper: VenueAccountsProvider })
  expect(second.result.current.accounts).toEqual({ ...empty, pacifica: solanaA })
})

it('keeps malformed existing storage empty without repeating migration', () => {
  wallets.evm = evmA
  localStorage.setItem(venueAccountsKey, '{invalid')
  const { result } = renderHook(useVenueAccounts, { wrapper: MigrationProvider })
  expect(result.current.accounts).toEqual(empty)
  expect(wallets.restore).not.toHaveBeenCalled()
})

it('links in memory even when localStorage reads and writes throw', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied') })
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('denied') })
  const { result } = renderHook(useVenueAccounts, { wrapper: VenueAccountsProvider })
  act(() => result.current.link('aster', evmA))
  expect(result.current.accounts.aster).toBe(evmA)
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
  expect(result.current.authority.aster).toMatchObject({ address: evmB, readiness: 'connected_cannot_sign' })
  wallets.signMessage = async () => new Uint8Array(64)
  wallets.signTransaction = async () => {}
  rerender()
  expect(result.current.authority.pacifica.readiness).toBe('ready')
  expect(result.current.authority.phoenix.readiness).toBe('connected_cannot_sign')
  wallets.evm = null
  wallets.solana = null
  rerender()
  expect(result.current.authority.venueAuthorities.map(({ readiness }) => readiness)).toEqual(Array(4).fill('not_connected'))
  expect(result.current.registry.accounts).toEqual({ pacifica: solanaA, phoenix: solanaB, hyperliquid: evmA, aster: evmB })
  expect(result.current.authority.asterAddress).toBe(evmB)
  expect(result.current.authority.phoenixAddress).toBe(solanaB)
})
