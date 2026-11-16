import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import type { Venue } from '@/agents/types'
import { VenueAccountsContext, type VenueAccounts } from './VenueAccountsContext'
import { readVenueAccounts, validVenueAddress, writeVenueAccounts } from './venue-accounts-storage'

export function VenueAccountsProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState(readVenueAccounts)
  const { accounts, migrationPending } = state
  const link = useCallback((venue: Venue, address: string) => {
    if (!validVenueAddress(venue, address)) throw new Error(`Invalid ${venue} owner address`)
    setState((current) => ({ accounts: { ...current.accounts, [venue]: address }, migrationPending: false }))
  }, [])
  const unlink = useCallback((venue: Venue) => {
    setState((current) => ({ accounts: { ...current.accounts, [venue]: null }, migrationPending: false }))
  }, [])
  const finishMigration = useCallback((bindings: Partial<VenueAccounts>) => {
    setState((current) => {
      if (!current.migrationPending) return current
      const accounts = { ...current.accounts }
      for (const venue of Object.keys(accounts) as Venue[]) {
        if (validVenueAddress(venue, bindings[venue])) accounts[venue] = bindings[venue]
      }
      return { accounts, migrationPending: false }
    })
  }, [])
  useEffect(() => { if (!migrationPending) writeVenueAccounts(accounts) }, [accounts, migrationPending])
  const value = useMemo(() => ({ accounts, link, unlink, migrationPending, finishMigration }), [accounts, link, unlink, migrationPending, finishMigration])

  return <VenueAccountsContext.Provider value={value}>{children}</VenueAccountsContext.Provider>
}
