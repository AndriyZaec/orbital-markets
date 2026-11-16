import { PublicKey } from '@solana/web3.js'
import type { Venue } from '@/agents/types'
import type { VenueAccounts } from './VenueAccountsContext'

export const venueAccountsKey = 'orbital.venueAccounts.v1'
export const emptyVenueAccounts: VenueAccounts = { pacifica: null, hyperliquid: null, aster: null, phoenix: null }

export function validVenueAddress(venue: Venue, address: unknown): address is string {
  if (typeof address !== 'string') return false
  if (venue === 'hyperliquid' || venue === 'aster') return /^0x[0-9a-fA-F]{40}$/.test(address)
  try { new PublicKey(address); return true } catch { return false }
}

export function readVenueAccounts(): { accounts: VenueAccounts; migrationPending: boolean } {
  let raw: string | null = null
  try { raw = window.localStorage.getItem(venueAccountsKey) } catch { /* Use in-memory bindings when storage is unavailable. */ }
  const accounts = { ...emptyVenueAccounts }
  try {
    const value: unknown = raw === null ? null : JSON.parse(raw)
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      for (const venue of Object.keys(accounts) as Venue[]) {
        const address = (value as Record<string, unknown>)[venue]
        if (validVenueAddress(venue, address)) accounts[venue] = address
      }
    }
  } catch { /* Invalid saved bindings are discarded, not migrated again. */ }
  return { accounts, migrationPending: raw === null }
}

export function writeVenueAccounts(accounts: VenueAccounts): void {
  try { window.localStorage.setItem(venueAccountsKey, JSON.stringify(accounts)) } catch { /* The current session can still link accounts. */ }
}
