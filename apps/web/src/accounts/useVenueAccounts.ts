import { useContext } from 'react'
import { VenueAccountsContext } from './VenueAccountsContext'

export function useVenueAccounts() {
  const value = useContext(VenueAccountsContext)
  if (!value) throw new Error('useVenueAccounts must be used within VenueAccountsProvider')
  return value
}
