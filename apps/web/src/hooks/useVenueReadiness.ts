import { useMemo } from 'react'
import { useVenueAuthority, type SigningReadiness } from './useVenueAuthority'
import { useLiveBalances } from './useLiveBalances'

// Single typed readiness layer for Pacifica + Hyperliquid. Composes the
// existing wallet-authority hook and the live-balances hook so the rest of
// the app has one shape to reason about. This hook is purely derivational —
// it does NOT open new connections, subscribe to new endpoints, or duplicate
// wallet logic. Freshness/staleness of balances is intentionally out of
// scope; a later backend step can add it via the same shape.

export type VenueId = 'pacifica' | 'hyperliquid'

export type VenueStatus =
  | 'disconnected'      // no wallet
  | 'wallet_connected'  // wallet present but signer or balance not yet ready
  | 'signer_missing'    // wallet cannot produce required signatures
  | 'balance_pending'   // signer OK; waiting on balance stream
  | 'ready'             // wallet + signer + balance stream all present
  | 'error'             // authority reports an error

export interface VenueReadiness {
  venue: VenueId
  label: string
  address: string | null
  shortAddress: string | null
  walletConnected: boolean
  signerReady: boolean
  balanceConnected: boolean
  balanceReady: boolean
  equity: number | null
  available: number | null
  status: VenueStatus
  blockingReasons: string[]
}

export interface AggregateReadiness {
  allReady: boolean
  readyCount: number
  totalCount: number
  blockingReasons: string[]
  statusLabel: 'Ready' | 'Needs attention' | 'Not connected'
}

export interface UseVenueReadinessResult {
  pacifica: VenueReadiness
  hyperliquid: VenueReadiness
  venues: VenueReadiness[]
  aggregate: AggregateReadiness
}

const LABELS: Record<VenueId, string> = {
  pacifica: 'Pacifica',
  hyperliquid: 'Hyperliquid',
}

function shorten(addr: string | null): string | null {
  if (!addr) return null
  if (addr.length <= 10) return addr
  return `${addr.slice(0, 4)}…${addr.slice(-4)}`
}

// Map SigningReadiness → the fine-grained pieces we expose. Kept small so
// authority-level changes stay isolated to one function.
function fromAuthority(readiness: SigningReadiness): {
  walletConnected: boolean
  signerReady: boolean
  errored: boolean
} {
  switch (readiness) {
    case 'ready':
      return { walletConnected: true, signerReady: true, errored: false }
    case 'connected_cannot_sign':
      return { walletConnected: true, signerReady: false, errored: false }
    case 'error':
      return { walletConnected: false, signerReady: false, errored: true }
    case 'not_connected':
    default:
      return { walletConnected: false, signerReady: false, errored: false }
  }
}

function buildReadiness(args: {
  venue: VenueId
  address: string | null
  authorityReadiness: SigningReadiness
  balance: { connected: boolean; equity: number; available: number }
}): VenueReadiness {
  const { venue, address, authorityReadiness, balance } = args
  const { walletConnected, signerReady, errored } = fromAuthority(authorityReadiness)

  // A live-balance connection is only meaningful once the signer is present;
  // treat it as "ready" only when both the venue reports connected AND we
  // actually have a signer (since balances tie to that wallet).
  const balanceConnected = balance.connected
  const balanceReady = balance.connected && signerReady

  const blockingReasons: string[] = []
  if (errored) blockingReasons.push('Wallet reported an error')
  if (!walletConnected) blockingReasons.push('Wallet not connected')
  else if (!signerReady) blockingReasons.push('Wallet cannot sign required messages')
  else if (!balanceReady) blockingReasons.push('Waiting on balance stream')

  let status: VenueStatus
  if (errored) status = 'error'
  else if (!walletConnected) status = 'disconnected'
  else if (!signerReady) status = 'signer_missing'
  else if (!balanceReady) status = 'balance_pending'
  else status = 'ready'

  return {
    venue,
    label: LABELS[venue],
    address,
    shortAddress: shorten(address),
    walletConnected,
    signerReady,
    balanceConnected,
    balanceReady,
    equity: balance.connected ? balance.equity : null,
    available: balance.connected ? balance.available : null,
    status,
    blockingReasons,
  }
}

export function useVenueReadiness(): UseVenueReadinessResult {
  const authority = useVenueAuthority()
  const balances = useLiveBalances()

  return useMemo<UseVenueReadinessResult>(() => {
    const pacifica = buildReadiness({
      venue: 'pacifica',
      address: authority.pacifica.address,
      authorityReadiness: authority.pacifica.readiness,
      balance: balances.pacifica,
    })
    const hyperliquid = buildReadiness({
      venue: 'hyperliquid',
      address: authority.hyperliquid.address,
      authorityReadiness: authority.hyperliquid.readiness,
      balance: balances.hyperliquid,
    })
    const venues = [pacifica, hyperliquid]
    const readyCount = venues.filter((v) => v.status === 'ready').length
    const totalCount = venues.length
    const allReady = readyCount === totalCount

    // Aggregate blocking reasons are prefixed with the venue label so the UI
    // can render a flat list without losing context.
    const blockingReasons = venues.flatMap((v) =>
      v.blockingReasons.map((r) => `${v.label}: ${r}`),
    )

    let statusLabel: AggregateReadiness['statusLabel']
    if (allReady) statusLabel = 'Ready'
    else if (venues.every((v) => v.status === 'disconnected')) statusLabel = 'Not connected'
    else statusLabel = 'Needs attention'

    return {
      pacifica,
      hyperliquid,
      venues,
      aggregate: {
        allReady,
        readyCount,
        totalCount,
        blockingReasons,
        statusLabel,
      },
    }
  }, [authority.pacifica, authority.hyperliquid, balances])
}
