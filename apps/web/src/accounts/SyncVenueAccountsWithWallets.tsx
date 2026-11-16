import { useEffect } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useAccount } from 'wagmi'
import { useVenueAccounts } from './useVenueAccounts'

// Temporary step-1 bridge: preserve wallet-bound accounts until explicit linking
// replaces this component. The registry itself is independent of wallet state.
export function SyncVenueAccountsWithWallets() {
  const solana = useWallet()
  const evm = useAccount()
  const { link, unlink } = useVenueAccounts()
  const evmAddress = evm.isConnected ? evm.address ?? null : null
  const solanaAddress = solana.connected ? solana.publicKey?.toBase58() ?? null : null

  useEffect(() => {
    if (evmAddress) {
      link('hyperliquid', evmAddress)
      link('aster', evmAddress)
    } else {
      unlink('hyperliquid')
      unlink('aster')
    }
  }, [evmAddress, link, unlink])

  useEffect(() => {
    if (solanaAddress) {
      link('pacifica', solanaAddress)
      link('phoenix', solanaAddress)
    } else {
      unlink('pacifica')
      unlink('phoenix')
    }
  }, [solanaAddress, link, unlink])

  return null
}
