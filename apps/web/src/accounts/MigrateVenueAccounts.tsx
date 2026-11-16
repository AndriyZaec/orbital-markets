import { useEffect } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useAccount } from 'wagmi'
import { browserStorage } from '@/agents/browser-storage'
import type { Venue } from '@/agents/types'
import { useVenueAccounts } from './useVenueAccounts'
import type { VenueAccounts } from './VenueAccountsContext'

// One-time legacy import. Later wallet connects/account changes never relink venues.
export function MigrateVenueAccounts() {
  const solana = useWallet()
  const evm = useAccount()
  const { migrationPending, finishMigration } = useVenueAccounts()
  const evmAddress = evm.isConnected ? evm.address ?? null : null
  const solanaAddress = solana.connected ? solana.publicKey?.toBase58() ?? null : null

  useEffect(() => {
    if (!migrationPending || (!evmAddress && !solanaAddress)) return
    let active = true
    const migrate = async () => {
      const bindings: Partial<VenueAccounts> = {}
      const storage = browserStorage()
      const candidates: Array<[Venue, string | null]> = [
        ['pacifica', solanaAddress], ['phoenix', solanaAddress], ['hyperliquid', evmAddress], ['aster', evmAddress],
      ]
      await Promise.all(candidates.map(async ([venue, address]) => {
        if (!address) return
        try { if (await storage.restore(venue, address)) bindings[venue] = address } catch { /* A missing/unreadable agent does not create a binding. */ }
      }))
      if (active) finishMigration(bindings)
    }
    void migrate()
    return () => { active = false }
  }, [migrationPending, finishMigration, evmAddress, solanaAddress])

  return null
}
