import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { PublicKey } from '@solana/web3.js'
import type { Venue } from '@/agents/types'
import { VenueAccountsContext, type VenueAccounts } from './VenueAccountsContext'

export function VenueAccountsProvider({ children }: { children: ReactNode }) {
  const [accounts, setAccounts] = useState<VenueAccounts>({ pacifica: null, hyperliquid: null, aster: null, phoenix: null })
  const link = useCallback((venue: Venue, address: string) => {
    if (venue === 'hyperliquid' || venue === 'aster') {
      if (!/^0x[0-9a-fA-F]{40}$/.test(address)) throw new Error(`Invalid ${venue} owner address`)
    } else {
      try {
        new PublicKey(address)
      } catch {
        throw new Error(`Invalid ${venue} owner address`)
      }
    }
    setAccounts((current) => current[venue] === address ? current : { ...current, [venue]: address })
  }, [])
  const unlink = useCallback((venue: Venue) => {
    setAccounts((current) => current[venue] === null ? current : { ...current, [venue]: null })
  }, [])
  const value = useMemo(() => ({ accounts, link, unlink }), [accounts, link, unlink])

  return <VenueAccountsContext.Provider value={value}>{children}</VenueAccountsContext.Provider>
}
