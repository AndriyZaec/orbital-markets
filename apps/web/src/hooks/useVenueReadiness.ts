import { createContext, createElement, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { apiError, apiFetch, userErrorMessage } from '@/lib/api'
import { useVenueAuthority, type SigningReadiness } from './useVenueAuthority'
import { useLiveBalances, type BalanceObservation } from './useLiveBalances'
import { useTradingAgents } from './useTradingAgents'
import type { TradingAgentState } from '@/agents/types'
import { trackAnalytics } from '@/lib/analytics'
import { liveAccountsKey, liveVenueBindingsBody, type VenueAddressMap } from '@/lib/live-bindings'
import { accountRead } from '@/lib/account-read'

// Single typed readiness layer for supported venues. Composes the
// existing wallet-authority hook and the live-balances hook so the rest of
// the app has one shape to reason about. This hook is purely derivational —
// it does NOT open new connections, subscribe to new endpoints, or duplicate
// wallet logic. Freshness/staleness of balances is intentionally out of
// scope; a later backend step can add it via the same shape.

export type VenueId = 'pacifica' | 'hyperliquid' | 'aster' | 'phoenix'

export type VenueStatus =
  | 'disconnected'       // no wallet
  | 'wallet_connected'   // wallet present but signer or balance not yet ready
  | 'signer_missing'     // wallet cannot produce required signatures
  | 'agent_missing'      // owner wallet has not authorized a local trading agent
  | 'agent_authorizing'  // owner authorization is in progress
  | 'balance_pending'    // signer OK; waiting on first account snapshot
  | 'account_stale'      // snapshot present but too old
  | 'unavailable'        // authorization may be tested, but live execution is not enabled
  | 'ready'              // wallet + signer + fresh account state
  | 'error'              // authority reports an error

export interface VenueReadiness {
  venue: VenueId
  label: string
  address: string | null
  shortAddress: string | null
  walletConnected: boolean
  signerReady: boolean
  agentReady: boolean
  agentAddress: string | null
  agentStatus: TradingAgentState['status']
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
  balanceObservation?: BalanceObservation
  status: VenueStatus
  blockingReasons: string[]
}

export interface AggregateReadiness {
  tradingReady: boolean
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
  aster: VenueReadiness
  phoenix: VenueReadiness
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

const VenueReadinessContext = createContext<UseVenueReadinessResult | null>(null)

const LABELS: Record<VenueId, string> = {
  pacifica: 'Pacifica',
  hyperliquid: 'Hyperliquid',
  aster: 'Aster',
  phoenix: 'Phoenix',
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
  agent: TradingAgentState
  balance: {
    connected: boolean
    equity: number
    available: number
    stream_ready?: boolean
    fresh?: boolean
    age_seconds?: number
    last_updated?: string
    reason?: string
    unavailable?: boolean
    observation?: BalanceObservation
    phoenix?: { live_ready: boolean; live_unavailable_reason?: string; registered_agent?: string; action_verification_required?: boolean }
  }
  unavailableReason?: string
}): VenueReadiness {
  const { venue, address, authorityReadiness, agent, balance } = args
  // Phoenix live_ready permits preparation; chain admission is checked on action.
  const unavailableReason = args.unavailableReason ?? (balance.unavailable ? balance.reason : undefined) ??
    (venue === 'phoenix' && balance.phoenix?.registered_agent && balance.phoenix.registered_agent !== agent.agentAddress ? 'Phoenix local agent differs from the registered agent. Reauthorize Phoenix.' : undefined) ??
    (venue === 'phoenix' && balance.phoenix?.live_ready !== true ? balance.phoenix?.live_unavailable_reason || 'Phoenix live readiness verification required' : undefined)
  const { walletConnected, signerReady, errored } = fromAuthority(authorityReadiness)
  const ownerMatches = venue === 'hyperliquid' || venue === 'aster'
    ? agent.ownerAddress?.toLowerCase() === address?.toLowerCase()
    : agent.ownerAddress === address
  const agentReady = agent.status === 'ready' && ownerMatches && !!agent.agentAddress

  const balanceConnected = balance.connected
  // Legacy backends without stream_ready/fresh: fall back to `connected` so
  // the readiness layer still behaves sensibly. New backends drive both.
  const streamReady = balance.stream_ready ?? balance.connected
  const accountFresh = balance.fresh ?? balance.connected
  const balanceReady = balanceConnected && signerReady && streamReady && accountFresh && !unavailableReason

  const blockingReasons: string[] = []
  if (errored) blockingReasons.push('Wallet reported an error')
  if (!walletConnected) blockingReasons.push('Wallet not connected')
  else if (!signerReady) blockingReasons.push('Wallet cannot sign required messages')
  else if (agent.status === 'restoring') blockingReasons.push('Restoring authorization...')
  else if (agent.status === 'authorizing') blockingReasons.push('Authorization in progress')
  else if (agent.status === 'error') blockingReasons.push(agent.error || 'Authorization failed')
  else if (!agentReady) blockingReasons.push('Authorization required')
  else if (unavailableReason) blockingReasons.push(unavailableReason)
  else if (!streamReady) blockingReasons.push(balance.reason || 'Waiting on account data stream')
  else if (!accountFresh) blockingReasons.push(balance.reason || 'Account data stale — refreshing')

  let status: VenueStatus
  if (errored) status = 'error'
  else if (!walletConnected) status = 'disconnected'
  else if (!signerReady) status = 'signer_missing'
  else if (agent.status === 'restoring' || agent.status === 'authorizing') status = 'agent_authorizing'
  else if (!agentReady) status = agent.status === 'error' ? 'error' : 'agent_missing'
  else if (unavailableReason) status = 'unavailable'
  else if (!streamReady) status = 'balance_pending'
  else if (!balanceConnected || !accountFresh) status = 'account_stale'
  else status = 'ready'

  return {
    venue,
    label: LABELS[venue],
    address,
    shortAddress: shorten(address),
    walletConnected,
    signerReady,
    agentReady,
    agentAddress: agent.agentAddress,
    agentStatus: agent.status,
    balanceConnected,
    balanceReady,
    streamReady,
    accountFresh,
    ageSeconds: typeof balance.age_seconds === 'number' ? balance.age_seconds : null,
    lastUpdated: balance.last_updated ?? null,
    // Only surface a numeric balance when the wallet itself is connected.
    // Backend subscribers keep running after wallet disconnect and can
    // report `connected: true` with 0/0; the wallet is the authoritative
    // signal for "does this user actually have this account right now".
    equity: walletConnected && balanceReady && !unavailableReason ? balance.equity : null,
    available: walletConnected && balanceReady && !unavailableReason ? balance.available : null,
    balanceObservation: walletConnected ? balance.observation : undefined,
    status,
    blockingReasons,
  }
}

function useVenueReadinessState(): UseVenueReadinessResult {
  const authority = useVenueAuthority()
  const tradingAgents = useTradingAgents()
  const pacAddr = authority.pacifica.address
  const hlAddr = authority.hyperliquid.address
  const balances = useLiveBalances(pacAddr, hlAddr, authority.aster.address, authority.phoenix.address)

  // Ensure state — kick /live/accounts/ensure once per (pacAddr|hlAddr) pair
  // so backend account subscribers can start BEFORE Execute Live. Without
  // this the UI deadlocks: readiness blocks Execute Live, but Execute Live
  // (via /live/prepare) is the only thing that starts the streams.
  const [ensureStatus, setEnsureStatus] = useState<EnsureStatus>('idle')
  const [ensureError, setEnsureError] = useState<string | null>(null)
  const inflightRef = useRef(new Map<string, { promise: Promise<boolean>; signal?: AbortSignal }>())
  const ensuredRef = useRef(new Set<string>())
  const readyPairsTrackedRef = useRef<Set<string>>(new Set())

  const pacSignerReady = authority.pacifica.readiness === 'ready'
  const hlSignerReady = authority.hyperliquid.readiness === 'ready'
  const asterSignerReady = authority.aster.readiness === 'ready'
  const ensureBindings = useMemo<VenueAddressMap>(() => Object.fromEntries([
    ['pacifica', pacSignerReady ? pacAddr : null],
    ['hyperliquid', hlSignerReady ? hlAddr : null],
    ['aster', asterSignerReady ? authority.aster.address : null],
    ['phoenix', authority.phoenix.readiness === 'ready' ? authority.phoenix.address : null],
  ].filter((entry): entry is [string, string] => !!entry[1])), [asterSignerReady, authority.aster.address, authority.phoenix.address, authority.phoenix.readiness, hlAddr, hlSignerReady, pacAddr, pacSignerReady])
  const currentBindings = useRef('')
  currentBindings.current = liveAccountsKey(ensureBindings)

  const refetchBalances = balances.refetch
  const doEnsure = useCallback((accounts: VenueAddressMap, signal?: AbortSignal): Promise<boolean> => {
    const pair = liveAccountsKey(accounts)
    const existing = inflightRef.current.get(pair)
    if (existing && !existing.signal?.aborted) return existing.promise
    setEnsureStatus('starting')
    setEnsureError(null)
    const entry = { promise: Promise.resolve(false), signal }
    inflightRef.current.set(pair, entry)
    const operation = (async () => {
    try {
      // The read-only endpoint isolates startup failures per venue.
      await accountRead(async (readSignal) => {
        const resp = await apiFetch('/api/v1/live/accounts/ensure', {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(liveVenueBindingsBody(accounts)), signal: readSignal,
        })
        if (!resp.ok) {
          const body = await resp.json().catch(() => ({}))
          throw apiError(resp.status, 'Unable to connect account data. Please try again.', body)
        }
      }, signal)
      if (signal?.aborted || currentBindings.current !== pair) return false
      ensuredRef.current.add(pair)
      setEnsureStatus('ready')
      // Nudge balances so readiness can move to ready without waiting for
      // the next fallback poll.
      refetchBalances().catch(() => {})
      return true
    } catch (e) {
      if (!signal?.aborted && currentBindings.current === pair) {
        setEnsureStatus('error')
        setEnsureError(userErrorMessage(e, 'Unable to connect account data. Please try again.'))
      }
      return false
    } finally {
      if (inflightRef.current.get(pair) === entry) inflightRef.current.delete(pair)
    }
    })()
    entry.promise = operation
    return operation
  }, [refetchBalances])

  // Identity owns startup. Failed attempts retry; successful startup is reused.
  useEffect(() => {
    if (Object.keys(ensureBindings).length === 0) return
    const pair = liveAccountsKey(ensureBindings)
    if (ensuredRef.current.has(pair)) return
    const controller = new AbortController()
    let retry = 0
    const attempt = async () => {
      const ready = await doEnsure(ensureBindings, controller.signal)
      if (!ready && !controller.signal.aborted) retry = window.setTimeout(() => void attempt(), 5_000)
    }
    void attempt()
    return () => { controller.abort(); window.clearTimeout(retry) }
  }, [doEnsure, ensureBindings])

  const ensureAccounts = useCallback(async () => {
    if (Object.keys(ensureBindings).length === 0) return
    // Manual retry: bypass the attempted-set gate.
    await doEnsure(ensureBindings)
  }, [doEnsure, ensureBindings])

  const value = useMemo<UseVenueReadinessResult>(() => {
    const pacifica = buildReadiness({
      venue: 'pacifica',
      address: authority.pacifica.address,
      authorityReadiness: authority.pacifica.readiness,
      agent: tradingAgents.pacifica,
      balance: balances.pacifica,
    })
    const hyperliquid = buildReadiness({
      venue: 'hyperliquid',
      address: authority.hyperliquid.address,
      authorityReadiness: authority.hyperliquid.readiness,
      agent: tradingAgents.hyperliquid,
      balance: balances.hyperliquid,
    })
    const aster = buildReadiness({
      venue: 'aster',
      address: authority.aster.address,
      authorityReadiness: authority.aster.readiness,
      agent: tradingAgents.aster,
      balance: balances.aster,
    })
    const phoenix = buildReadiness({ venue: 'phoenix', address: authority.phoenix.address,
      authorityReadiness: authority.phoenix.readiness, agent: tradingAgents.phoenix, balance: balances.phoenix })
    const venues = [pacifica, hyperliquid, aster, phoenix]
    const readyCount = venues.filter((v) => v.status === 'ready').length
    const totalCount = venues.length
    const tradingReady = readyCount >= 2

    // Aggregate blocking reasons are prefixed with the venue label so the UI
    // can render a flat list without losing context.
    const blockingReasons = venues.flatMap((v) =>
      v.blockingReasons.map((r) => `${v.label}: ${r}`),
    )

    let statusLabel: AggregateReadiness['statusLabel']
    if (tradingReady) statusLabel = 'Ready'
    else if (venues.every((v) => v.status === 'disconnected')) statusLabel = 'Not connected'
    else statusLabel = 'Needs attention'

    return {
      pacifica,
      hyperliquid,
      aster,
      phoenix,
      venues,
      ensureStatus,
      ensureError,
      ensureAccounts,
      refreshBalances: balances.refetch,
      aggregate: {
        tradingReady,
        readyCount,
        totalCount,
        blockingReasons,
        statusLabel,
      },
    }
  }, [authority.pacifica, authority.hyperliquid, authority.aster, authority.phoenix, tradingAgents.pacifica, tradingAgents.hyperliquid, tradingAgents.aster, tradingAgents.phoenix, balances, ensureStatus, ensureError, ensureAccounts])

  useEffect(() => {
    const readyVenues = value.venues.filter((venue) => venue.status === 'ready' && venue.address)
    readyVenues.forEach((first, index) => {
      readyVenues.slice(index + 1).forEach((second) => {
        const key = `${first.venue}:${first.address}|${second.venue}:${second.address}`
        if (readyPairsTrackedRef.current.has(key)) return
        readyPairsTrackedRef.current.add(key)
        trackAnalytics('accounts_ready', { venue_pair: `${first.venue}_${second.venue}` })
      })
    })
  }, [value.venues])

  return value
}

export function VenueReadinessProvider({ children }: { children: ReactNode }) {
  const value = useVenueReadinessState()
  return createElement(VenueReadinessContext.Provider, { value }, children)
}

export function useVenueReadiness(): UseVenueReadinessResult {
  const value = useContext(VenueReadinessContext)
  if (!value) throw new Error('useVenueReadiness must be used within VenueReadinessProvider')
  return value
}
