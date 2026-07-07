import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { apiFetch } from '@/lib/api'
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
  | 'disconnected'       // no wallet
  | 'wallet_connected'   // wallet present but signer or balance not yet ready
  | 'signer_missing'     // wallet cannot produce required signatures
  | 'balance_pending'    // signer OK; waiting on first account snapshot
  | 'account_stale'      // snapshot present but too old
  | 'ready'              // wallet + signer + fresh account state
  | 'error'              // authority reports an error

export interface VenueReadiness {
  venue: VenueId
  label: string
  address: string | null
  shortAddress: string | null
  walletConnected: boolean
  signerReady: boolean
  balanceConnected: boolean
  balanceReady: boolean
  // Backend account-data freshness. streamReady: subscriber has produced at
  // least one snapshot. accountFresh: within the backend's freshness window.
  // ageSeconds / lastUpdated echo the balance response for UI display.
  streamReady: boolean
  accountFresh: boolean
  ageSeconds: number | null
  lastUpdated: string | null
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

// Status of the backend account-stream ensure step. This is per-session
// UI state, not persisted — a fresh page load starts idle again.
export type EnsureStatus = 'idle' | 'starting' | 'ready' | 'error'

export interface UseVenueReadinessResult {
  pacifica: VenueReadiness
  hyperliquid: VenueReadiness
  venues: VenueReadiness[]
  aggregate: AggregateReadiness
  ensureStatus: EnsureStatus
  ensureError: string | null
  ensureAccounts: () => Promise<void>
  // Force a balances re-fetch. Callers use this on user intent (opening
  // the trade panel, clicking Execute Live) — the background 30s poll is
  // deliberately slow, so intent-driven refresh is how we stay accurate at
  // the moments that matter.
  refreshBalances: () => Promise<void>
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
  balance: {
    connected: boolean
    equity: number
    available: number
    stream_ready?: boolean
    fresh?: boolean
    age_seconds?: number
    last_updated?: string
    reason?: string
  }
}): VenueReadiness {
  const { venue, address, authorityReadiness, balance } = args
  const { walletConnected, signerReady, errored } = fromAuthority(authorityReadiness)

  const balanceConnected = balance.connected
  // Legacy backends without stream_ready/fresh: fall back to `connected` so
  // the readiness layer still behaves sensibly. New backends drive both.
  const streamReady = balance.stream_ready ?? balance.connected
  const accountFresh = balance.fresh ?? balance.connected
  const balanceReady = balanceConnected && signerReady && streamReady && accountFresh

  const blockingReasons: string[] = []
  if (errored) blockingReasons.push('Wallet reported an error')
  if (!walletConnected) blockingReasons.push('Wallet not connected')
  else if (!signerReady) blockingReasons.push('Wallet cannot sign required messages')
  else if (!streamReady) blockingReasons.push(balance.reason || 'Waiting on account data stream')
  else if (!accountFresh) blockingReasons.push(balance.reason || 'Account data stale — refreshing')

  let status: VenueStatus
  if (errored) status = 'error'
  else if (!walletConnected) status = 'disconnected'
  else if (!signerReady) status = 'signer_missing'
  else if (!streamReady) status = 'balance_pending'
  else if (!accountFresh) status = 'account_stale'
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
    streamReady,
    accountFresh,
    ageSeconds: typeof balance.age_seconds === 'number' ? balance.age_seconds : null,
    lastUpdated: balance.last_updated ?? null,
    equity: balance.connected ? balance.equity : null,
    available: balance.connected ? balance.available : null,
    status,
    blockingReasons,
  }
}

export function useVenueReadiness(): UseVenueReadinessResult {
  const authority = useVenueAuthority()
  const balances = useLiveBalances()

  // Ensure state — kick /live/accounts/ensure once per (pacAddr|hlAddr) pair
  // so backend account subscribers can start BEFORE Execute Live. Without
  // this the UI deadlocks: readiness blocks Execute Live, but Execute Live
  // (via /live/prepare) is the only thing that starts the streams.
  const [ensureStatus, setEnsureStatus] = useState<EnsureStatus>('idle')
  const [ensureError, setEnsureError] = useState<string | null>(null)
  const inflightRef = useRef<string | null>(null)   // pair currently being ensured
  const attemptedRef = useRef<Set<string>>(new Set()) // pairs already auto-attempted

  const pacAddr = authority.pacifica.address
  const hlAddr = authority.hyperliquid.address
  const pacSignerReady = authority.pacifica.readiness === 'ready'
  const hlSignerReady = authority.hyperliquid.readiness === 'ready'

  const doEnsure = useCallback(async (pac: string, hl: string) => {
    const pair = `${pac}|${hl}`
    if (inflightRef.current === pair) return // dedup concurrent calls
    inflightRef.current = pair
    setEnsureStatus('starting')
    setEnsureError(null)
    try {
      const resp = await apiFetch('/api/v1/live/accounts/ensure', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ account_pacifica: pac, account_hyperliquid: hl }),
      })
      if (!resp.ok) {
        const b = await resp.json().catch(() => ({}))
        throw new Error(b.error || `HTTP ${resp.status}`)
      }
      setEnsureStatus('ready')
      // Nudge balances so readiness can move to ready without waiting for
      // the next 5s poll tick.
      balances.refetch().catch(() => {})
    } catch (e) {
      setEnsureStatus('error')
      setEnsureError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      inflightRef.current = null
    }
  }, [balances])

  // Auto-start streams once per address pair as soon as BOTH signers are
  // ready. Manual retry (ensureAccounts) bypasses the attempted-set check.
  useEffect(() => {
    if (!pacAddr || !hlAddr || !pacSignerReady || !hlSignerReady) return
    const pair = `${pacAddr}|${hlAddr}`
    if (attemptedRef.current.has(pair)) return
    attemptedRef.current.add(pair)
    doEnsure(pacAddr, hlAddr)
  }, [pacAddr, hlAddr, pacSignerReady, hlSignerReady, doEnsure])

  const ensureAccounts = useCallback(async () => {
    if (!pacAddr || !hlAddr) return
    // Manual retry: bypass the attempted-set gate.
    await doEnsure(pacAddr, hlAddr)
  }, [pacAddr, hlAddr, doEnsure])

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
      ensureStatus,
      ensureError,
      ensureAccounts,
      refreshBalances: balances.refetch,
      aggregate: {
        allReady,
        readyCount,
        totalCount,
        blockingReasons,
        statusLabel,
      },
    }
  }, [authority.pacifica, authority.hyperliquid, balances, ensureStatus, ensureError, ensureAccounts])
}
