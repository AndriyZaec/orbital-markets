import { createContext } from 'react'
import type { Venue } from '@/agents/types'

export type VenueAccounts = Record<Venue, string | null>

export interface VenueAccountsManager {
  accounts: VenueAccounts
  link(venue: Venue, address: string): void
  unlink(venue: Venue): void
}

export const VenueAccountsContext = createContext<VenueAccountsManager | null>(null)
