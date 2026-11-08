import { useMemo } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useAccount as useEvmAccount } from 'wagmi'

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

  const pacifica = useMemo<VenueAuthority>(() => {
    if (!solWallet.connected || !solWallet.publicKey) {
      return { venue: 'pacifica', readiness: 'not_connected', address: null, signerType: null, error: null }
    }
    if (!solWallet.signMessage) {
      return { venue: 'pacifica', readiness: 'connected_cannot_sign', address: solWallet.publicKey.toBase58(), signerType: 'solana', error: 'Wallet does not support message signing' }
    }
    return { venue: 'pacifica', readiness: 'ready', address: solWallet.publicKey.toBase58(), signerType: 'solana', error: null }
  }, [solWallet.connected, solWallet.publicKey, solWallet.signMessage])

  const hyperliquid = useMemo<VenueAuthority>(() => {
    if (!evmAccount.isConnected || !evmAccount.address) {
      return { venue: 'hyperliquid', readiness: 'not_connected', address: null, signerType: null, error: null }
    }
    return { venue: 'hyperliquid', readiness: 'ready', address: evmAccount.address, signerType: 'evm', error: null }
  }, [evmAccount.isConnected, evmAccount.address])

  const aster = useMemo<VenueAuthority>(() => {
    if (!evmAccount.isConnected || !evmAccount.address) {
      return { venue: 'aster', readiness: 'not_connected', address: null, signerType: null, error: null }
    }
    return { venue: 'aster', readiness: 'ready', address: evmAccount.address, signerType: 'evm', error: null }
  }, [evmAccount.isConnected, evmAccount.address])

  const phoenix = useMemo<VenueAuthority>(() => {
    const address = solWallet.connected ? solWallet.publicKey?.toBase58() ?? null : null
    if (!address) return { venue: 'phoenix', readiness: 'not_connected', address: null, signerType: null, error: null }
    return { venue: 'phoenix', readiness: solWallet.signTransaction ? 'ready' : 'connected_cannot_sign', address,
      signerType: 'solana', error: solWallet.signTransaction ? null : 'Wallet does not support transaction signing' }
  }, [solWallet.connected, solWallet.publicKey, solWallet.signTransaction])
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
