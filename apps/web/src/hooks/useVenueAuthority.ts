import { useMemo } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useAccount as useEvmAccount } from 'wagmi'
import { useVenueAccounts } from '@/accounts/useVenueAccounts'

export type SigningReadiness = 'not_connected' | 'connected_cannot_sign' | 'ready' | 'error'

export interface VenueAuthority {
  venue: string
  readiness: SigningReadiness
  address: string | null
  signerType: 'solana' | 'evm' | null
  error: string | null
}

export function useVenueAuthority() {
  const solWallet = useWallet()
  const evmAccount = useEvmAccount()
  const { accounts } = useVenueAccounts()

  const pacifica = useMemo<VenueAuthority>(() => {
    if (!solWallet.connected || !solWallet.publicKey) {
      return { venue: 'pacifica', readiness: 'not_connected', address: accounts.pacifica, signerType: null, error: null }
    }
    if (!solWallet.signMessage || solWallet.publicKey.toBase58() !== accounts.pacifica) {
      return { venue: 'pacifica', readiness: 'connected_cannot_sign', address: accounts.pacifica, signerType: 'solana', error: solWallet.signMessage ? null : 'Wallet does not support message signing' }
    }
    return { venue: 'pacifica', readiness: 'ready', address: accounts.pacifica, signerType: 'solana', error: null }
  }, [solWallet.connected, solWallet.publicKey, solWallet.signMessage, accounts.pacifica])

  const hyperliquid = useMemo<VenueAuthority>(() => {
    if (!evmAccount.isConnected || !evmAccount.address) {
      return { venue: 'hyperliquid', readiness: 'not_connected', address: accounts.hyperliquid, signerType: null, error: null }
    }
    return { venue: 'hyperliquid', readiness: evmAccount.address.toLowerCase() === accounts.hyperliquid?.toLowerCase() ? 'ready' : 'connected_cannot_sign', address: accounts.hyperliquid, signerType: 'evm', error: null }
  }, [evmAccount.isConnected, evmAccount.address, accounts.hyperliquid])

  const aster = useMemo<VenueAuthority>(() => {
    if (!evmAccount.isConnected || !evmAccount.address) {
      return { venue: 'aster', readiness: 'not_connected', address: accounts.aster, signerType: null, error: null }
    }
    return { venue: 'aster', readiness: evmAccount.address.toLowerCase() === accounts.aster?.toLowerCase() ? 'ready' : 'connected_cannot_sign', address: accounts.aster, signerType: 'evm', error: null }
  }, [evmAccount.isConnected, evmAccount.address, accounts.aster])

  const phoenix = useMemo<VenueAuthority>(() => {
    const address = accounts.phoenix
    if (!solWallet.connected || !solWallet.publicKey) return { venue: 'phoenix', readiness: 'not_connected', address, signerType: null, error: null }
    return { venue: 'phoenix', readiness: solWallet.signTransaction && solWallet.publicKey.toBase58() === address ? 'ready' : 'connected_cannot_sign', address,
      signerType: 'solana', error: solWallet.signTransaction ? null : 'Wallet does not support transaction signing' }
  }, [solWallet.connected, solWallet.publicKey, solWallet.signTransaction, accounts.phoenix])
  const venueAuthorities = useMemo(() => [pacifica, hyperliquid, aster, phoenix], [pacifica, hyperliquid, aster, phoenix])

  return {
    venueAuthorities,
    pacifica,
    hyperliquid,
    aster,
    phoenix,
    pacificaAddress: pacifica.address,
    hyperliquidAddress: hyperliquid.address,
    asterAddress: aster.address,
    phoenixAddress: phoenix.address,
  }
}
