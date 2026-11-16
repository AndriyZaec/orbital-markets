import type { ReactNode } from 'react'
import { VenueAccountsProvider } from '../src/accounts/VenueAccountsProvider'
import { SyncVenueAccountsWithWallets } from '../src/accounts/SyncVenueAccountsWithWallets'

export function WalletSyncedVenueAccounts({ children }: { children: ReactNode }) {
  return <VenueAccountsProvider><SyncVenueAccountsWithWallets />{children}</VenueAccountsProvider>
}
