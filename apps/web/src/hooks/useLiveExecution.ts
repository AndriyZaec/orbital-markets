import {
  createContext,
  createElement,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { apiError, apiFetch } from '@/lib/api'
import { subscribeLiveSessionEvents } from '@/lib/live-events'
import { liveAccountsQuery, liveVenueBindingsBody, type VenueAddressMap } from '@/lib/live-bindings'
import { useVenueAuthority } from './useVenueAuthority'
import type { SigningRequest, SignedAction } from '@/types/signing'
import type { Venue } from '@/agents/types'
import { useTradingAgents } from './useTradingAgents'
import {
  areValidLeg1SigningRequests,
  executionFailurePhase,
  executionPhaseFromStatus,
  normalizeHyperliquidAddress,
  normalizePacificaAddress,
  type AdvanceStatus,
} from '@/lib/live-execution-state'

// Two-phase non-custodial open (Option A):
//   prepare -> sign leg-1 open + leg-1 unwind -> advance (submit leg 1, wait fill)
//           -> sign leg-2 (sized from actual fill) -> advance (submit leg 2, verify)
// On any post-leg-1 failure the backend fires the pre-signed unwind.
export type ExecutionPhase =
  | 'idle'
  | 'preparing'
  | 'awaiting_leg1' // signing leg-1 open + unwind
  | 'submitting_leg1' // backend submitting leg 1 + waiting for fill
  | 'awaiting_leg2' // signing leg-2 (sized from actual fill)
  | 'submitting_leg2' // backend submitting leg 2 + verifying hedge
  | 'awaiting_leg2_retry' // signing the single residual hedge retry
  | 'submitting_leg2_retry' // submitting the residual retry
  | 'recovering' // venue truth is being reconciled after an ambiguous result
  | 'open' // success
  | 'degraded' // hedge broken; leg 1 unwound
  | 'aborted' // leg 1 underfilled or user-aborted; leg 1 unwound
  | 'failed' // leg 1 never opened, or signing failed before any submission

export interface LegFillView {
  filled_amount: number
  avg_price: number
  status: string
  fill_ratio?: number
}

export type UnwindStatus = 'not_armed' | 'skipped' | 'submit_failed' | 'unconfirmed' | 'confirmed' | null

export interface RemainingExposure {
  leg: number
  venue: string
  symbol: string
  side: string
  amount: number
}

export interface LiveExecutionState {
  phase: ExecutionPhase
  asset: string | null
  sessionId: string | null
  accountPacifica: string | null
  accountHyperliquid: string | null
  accounts: VenueAddressMap
  riskierVenue: string | null
  hedgeVenue: string | null
  leg1Requests: SigningRequest[] // [open, unwind]
  leg2Request: SigningRequest | null
  leg1Fill: LegFillView | null
  leg2Fill: LegFillView | null
  mismatch: number | null
  positionId: string | null
  unwound: boolean
  unwindStatus: UnwindStatus
  error: string | null
  reason: string | null
  expiresAt: string | null
  currentVenue: string | null
  remainingExposure: RemainingExposure[]
}

const INITIAL_STATE: LiveExecutionState = {
  phase: 'idle',
  asset: null,
  sessionId: null,
  accountPacifica: null,
  accountHyperliquid: null,
  accounts: {},
  riskierVenue: null,
  hedgeVenue: null,
  leg1Requests: [],
  leg2Request: null,
  leg1Fill: null,
  leg2Fill: null,
  mismatch: null,
  positionId: null,
  unwound: false,
  unwindStatus: null,
  error: null,
  reason: null,
  expiresAt: null,
  currentVenue: null,
  remainingExposure: [],
}

interface PrepareResp {
  session_id: string
  asset: string
  riskier_venue: string
  hedge_venue: string
  expires_at: string
  signing_requests: SigningRequest[] // [optional leverage updates, leg1 open, leg1 unwind]
}

interface AdvanceResp {
  session_id: string
  status: AdvanceStatus
  leg1_fill?: LegFillView
  leg2_fill?: LegFillView
  signing_requests?: SigningRequest[] // [leg2 open]
  mismatch?: number
  position_id?: string
  reason?: string
  unwound?: boolean
  unwind_status?: 'not_armed' | 'skipped' | 'submit_failed' | 'unconfirmed' | 'confirmed'
  remaining_exposure?: RemainingExposure[]
}

const recoveryTimeoutMs = 5 * 60_000
const recoveryFallbackPollMs = 5_000

function useLiveExecutionState() {
  const [state, setState] = useState<LiveExecutionState>(INITIAL_STATE)
  const { pacificaAddress, hyperliquidAddress, asterAddress } = useVenueAuthority()
  const tradingAgents = useTradingAgents()

  useEffect(() => {
    if (state.phase !== 'recovering' || !state.sessionId || Object.keys(state.accounts).length !== 2) return

    let cancelled = false
    let streamConnected = false
    let fallbackTimer = 0
    let deadlineTimer = 0
    const sessionId = state.sessionId
    const accounts = state.accounts
    const query = liveAccountsQuery(accounts)
    const apply = (result: AdvanceResp) => {
      if (cancelled || result.status === 'recovering') return false
      setState((current) => ({
        ...current,
        phase: executionPhaseFromStatus(result.status),
        leg1Fill: result.leg1_fill ?? current.leg1Fill,
        leg2Fill: result.leg2_fill ?? current.leg2Fill,
        mismatch: result.mismatch ?? current.mismatch,
        positionId: result.position_id ?? current.positionId,
        reason: result.reason ?? current.reason,
        unwound: result.unwound ?? false,
        unwindStatus: (result.unwind_status ?? null) as UnwindStatus,
        remainingExposure: result.remaining_exposure ?? [],
      }))
      return true
    }

    const closeStream = subscribeLiveSessionEvents(
      accounts,
      sessionId,
      (connected) => { streamConnected = connected },
      (data) => { if (apply(data as AdvanceResp)) closeStream() },
    )
    const pollFallback = async () => {
      if (cancelled) return
      if (!streamConnected) {
        try {
          const response = await apiFetch(`/api/v1/live/sessions/${sessionId}?${query}`)
          if (response.ok && apply(await response.json())) return
        } catch {
          // SSE reconnect and the next fallback poll remain active.
        }
      }
      fallbackTimer = window.setTimeout(pollFallback, recoveryFallbackPollMs)
    }
    fallbackTimer = window.setTimeout(pollFallback, recoveryFallbackPollMs)
    deadlineTimer = window.setTimeout(() => {
      if (!cancelled) setState((current) => ({
        ...current,
        phase: 'degraded',
        reason: 'Venue reconciliation did not finish in time. Review venue positions before retrying.',
      }))
    }, recoveryTimeoutMs)
    return () => {
      cancelled = true
      closeStream()
      window.clearTimeout(fallbackTimer)
      window.clearTimeout(deadlineTimer)
    }
  }, [state.phase, state.sessionId, state.accounts])

  // Live refs of the currently connected accounts. The executeLive async
  // callback is created once and closes over stale addresses; refs let us
  // read the LATEST connected accounts inside the flow without re-creating
  // the callback (which would cancel in-flight sessions).
  const accountsRef = useRef<Record<Venue, string | null>>({
    pacifica: pacificaAddress, hyperliquid: hyperliquidAddress, aster: asterAddress,
  })
  useEffect(() => {
    accountsRef.current = { pacifica: pacificaAddress, hyperliquid: hyperliquidAddress, aster: asterAddress }
  }, [asterAddress, hyperliquidAddress, pacificaAddress])

  const postAdvance = async (
    accounts: VenueAddressMap,
    body: Record<string, unknown>,
  ): Promise<AdvanceResp> => {
    const resp = await apiFetch('/api/v1/live/advance', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...body, ...liveVenueBindingsBody(accounts) }),
    })
    if (!resp.ok) {
      const b = await resp.json().catch(() => ({}))
      throw apiError(resp.status, 'Unable to continue execution. Check the session and try again.', b)
    }
    return resp.json()
  }

  const executeLive = useCallback(async (
    opportunityId: string,
    leverage: number,
    requestedNotional?: number,
    venues: [Venue, Venue] = ['pacifica', 'hyperliquid'],
    asterSymbol?: string,
  ) => {
    const authorityByVenue: Record<Venue, string | null> = {
      pacifica: pacificaAddress, hyperliquid: hyperliquidAddress, aster: asterAddress,
    }
    const agentByVenue = {
      pacifica: tradingAgents.pacifica,
      hyperliquid: tradingAgents.hyperliquid,
      aster: tradingAgents.aster,
    }
    if (venues[0] === venues[1] || venues.some((venue) => !authorityByVenue[venue])) {
      setState({ ...INITIAL_STATE, phase: 'failed', error: 'Both venue accounts must be connected' })
      return
    }
    if (venues.some((venue) => agentByVenue[venue].status !== 'ready' || !agentByVenue[venue].agentAddress)) {
      setState({ ...INITIAL_STATE, phase: 'failed', error: 'Authorize both venues first' })
      return
    }

    setState({ ...INITIAL_STATE, phase: 'preparing' })

    let exposurePossible = false
    const preparedAccounts = Object.fromEntries(venues.map((venue) => [venue, authorityByVenue[venue]!]))
    const preparedAgents = Object.fromEntries(venues.map((venue) => [venue, agentByVenue[venue].agentAddress!]))

    try {
      if (venues.includes('aster')) {
        if (!asterSymbol) throw new Error('Aster market symbol is unavailable')
        await tradingAgents.requestAster({ operation: 'get_leverage_brackets', symbol: asterSymbol })
      }
      // 1. Prepare — get session + leg-1 open & unwind signing requests.
      const prepResp = await apiFetch('/api/v1/live/prepare', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          opportunity_id: opportunityId,
          leverage,
          ...(typeof requestedNotional === 'number' && requestedNotional > 0
            ? { requested_notional: requestedNotional }
            : {}),
          ...liveVenueBindingsBody(
            preparedAccounts,
            preparedAgents,
          ),
        }),
      })
      if (!prepResp.ok) {
        const b: { error?: string; code?: string; reasons?: unknown } = await prepResp.json().catch(() => ({}))
        const reasons = Array.isArray(b.reasons)
          ? b.reasons.filter((reason): reason is string => typeof reason === 'string')
          : []
        const message = apiError(
          prepResp.status,
          'Unable to prepare live execution. Refresh the opportunity and try again.',
          b,
        ).message
        throw new Error(reasons.length > 0 ? `${message}: ${reasons.join('; ')}` : message)
      }
      const prep: PrepareResp = await prepResp.json()
      const leg1Requests = prep.signing_requests || []
      if (!areValidLeg1SigningRequests(leg1Requests, prep.riskier_venue, prep.hedge_venue)) {
        throw new Error('Expected valid leverage updates plus leg-1 open and unwind requests')
      }

      // Snapshot the accounts the session was prepared for. Any subsequent
      // wallet change must halt the flow (before leg 1) or trigger abort +
      // armed unwind (after leg 1). Comparisons are normalized (lowercased
      // for EVM, trimmed for Solana) so casing/whitespace doesn't false-flag.
      const normalizedAccounts = Object.fromEntries(venues.map((venue) => [
        venue,
        venue === 'pacifica'
          ? normalizePacificaAddress(preparedAccounts[venue])
          : normalizeHyperliquidAddress(preparedAccounts[venue]),
      ]))

      const detectAccountChange = (): string | null => {
        for (const venue of venues) {
          const current = venue === 'pacifica'
            ? normalizePacificaAddress(accountsRef.current[venue])
            : normalizeHyperliquidAddress(accountsRef.current[venue])
          if (current !== normalizedAccounts[venue]) {
            return venue === 'pacifica' ? 'Pacifica' : venue === 'aster' ? 'Aster' : 'Hyperliquid'
          }
        }
        return null
      }

      setState((s) => ({
        ...s,
        phase: 'awaiting_leg1',
        asset: prep.asset,
        sessionId: prep.session_id,
        accountPacifica: pacificaAddress,
        accountHyperliquid: hyperliquidAddress,
        accounts: preparedAccounts,
        riskierVenue: prep.riskier_venue,
        hedgeVenue: prep.hedge_venue,
        leg1Requests,
        expiresAt: prep.expires_at,
        currentVenue: prep.riskier_venue,
      }))

      // Guard: pre-leg-1 wallet swap → nothing submitted, fail cleanly.
      {
        const changed = detectAccountChange()
        if (changed) {
          setState((s) => ({
            ...s,
            phase: 'failed',
            error: `${changed} wallet changed before execution. Restart the trade.`,
          }))
          return
        }
      }

      // 2. Sign every prepare request up front. If any fails, submit nothing.
      let signedLeg1: SignedAction[]
      try {
        signedLeg1 = []
        for (const req of leg1Requests) {
          signedLeg1.push(await tradingAgents.sign(req))
        }
      } catch (e) {
        // Signing-failure rule: nothing submitted, abort cleanly.
        setState((s) => ({
          ...s,
          phase: 'failed',
          error: `Leg 1 signing failed: ${e instanceof Error ? e.message : 'unknown error'}`,
        }))
        return
      }

      // Guard: wallet swap during leg-1 signing but before we submit anything.
      // Nothing has hit the venue yet; same rule as above.
      {
        const changed = detectAccountChange()
        if (changed) {
          setState((s) => ({
            ...s,
            phase: 'failed',
            error: `${changed} wallet changed before execution. Restart the trade.`,
          }))
          return
        }
      }

      // 3. Advance step 1 — backend arms unwind, submits leg 1, waits for fill.
      setState((s) => ({ ...s, phase: 'submitting_leg1' }))
      exposurePossible = true
      let adv1: AdvanceResp
      try {
        adv1 = await postAdvance(preparedAccounts, { session_id: prep.session_id, signed_actions: signedLeg1 })
      } catch (e) {
        setState((s) => ({
          ...s,
          phase: 'recovering',
          reason: `Leg-1 response is uncertain: ${e instanceof Error ? e.message : 'unknown error'}`,
        }))
        return
      }

      if (adv1.status === 'aborted' || adv1.status === 'failed' || adv1.status === 'degraded' || adv1.status === 'recovering') {
        setState((s) => ({
          ...s,
          phase: adv1.status as ExecutionPhase,
          leg1Fill: adv1.leg1_fill ?? null,
          reason: adv1.reason ?? null,
          unwound: adv1.unwound ?? false,
          unwindStatus: (adv1.unwind_status ?? null) as UnwindStatus,
          remainingExposure: adv1.remaining_exposure ?? [],
        }))
        return
      }

      // status === 'awaiting_leg2_sign'
      const leg2Reqs = adv1.signing_requests || []
      if (leg2Reqs.length < 1) {
        throw new Error('Expected leg-2 signing request from backend')
      }
      const leg2Req = leg2Reqs[0]

      setState((s) => ({
        ...s,
        phase: 'awaiting_leg2',
        leg1Fill: adv1.leg1_fill ?? null,
        leg2Request: leg2Req,
        currentVenue: leg2Req.venue,
        expiresAt: leg2Req.expires_at,
      }))

      // Guard: wallet swap after leg 1 filled but before leg 2 signing.
      // Leg 1 is live on the venue; the backend has an armed unwind. Call
      // abort so it fires the pre-signed unwind. If abort itself fails, we
      // surface a degraded state — manual action may be required.
      {
        const changed = detectAccountChange()
        if (changed) {
          const abortResp = await postAdvance(preparedAccounts, { session_id: prep.session_id, abort: true }).catch(() => null)
          const abortOk = abortResp !== null
          setState((s) => ({
            ...s,
            phase: abortOk ? 'aborted' : 'degraded',
            reason: abortOk
              ? `Execution aborted: ${changed} wallet changed after leg 1. Armed unwind fired.`
              : `${changed} wallet changed after leg 1 and abort failed. Manual action may be required.`,
            unwound: abortResp?.unwound ?? false,
            unwindStatus: (abortResp?.unwind_status ?? (abortOk ? 'unconfirmed' : 'submit_failed')) as UnwindStatus,
          }))
          return
        }
      }

      // 4. Sign leg 2. If it fails, tell the backend to abort -> fires armed unwind.
      let signedLeg2: SignedAction
      try {
        signedLeg2 = await tradingAgents.sign(leg2Req)
      } catch (e) {
        const abortResp = await postAdvance(preparedAccounts, { session_id: prep.session_id, abort: true }).catch(() => null)
        setState((s) => ({
          ...s,
          phase: 'aborted',
          reason: `Leg 2 signing failed: ${e instanceof Error ? e.message : 'unknown error'}`,
          unwound: abortResp?.unwound ?? false,
          unwindStatus: (abortResp?.unwind_status ?? 'unconfirmed') as UnwindStatus,
        }))
        return
      }

      // Guard: wallet swap during leg-2 signing but before submitting.
      // Same treatment as the pre-leg-2-sign guard.
      {
        const changed = detectAccountChange()
        if (changed) {
          const abortResp = await postAdvance(preparedAccounts, { session_id: prep.session_id, abort: true }).catch(() => null)
          const abortOk = abortResp !== null
          setState((s) => ({
            ...s,
            phase: abortOk ? 'aborted' : 'degraded',
            reason: abortOk
              ? `Execution aborted: ${changed} wallet changed after leg 1. Armed unwind fired.`
              : `${changed} wallet changed after leg 1 and abort failed. Manual action may be required.`,
            unwound: abortResp?.unwound ?? false,
            unwindStatus: (abortResp?.unwind_status ?? (abortOk ? 'unconfirmed' : 'submit_failed')) as UnwindStatus,
          }))
          return
        }
      }

      // 5. Advance step 2 — submit leg 2, verify hedge.
      setState((s) => ({ ...s, phase: 'submitting_leg2' }))
      let adv2: AdvanceResp
      try {
        adv2 = await postAdvance(preparedAccounts, { session_id: prep.session_id, signed_actions: [signedLeg2] })
      } catch (e) {
        setState((s) => ({
          ...s,
          phase: 'recovering',
          reason: `Leg-2 response is uncertain: ${e instanceof Error ? e.message : 'unknown error'}`,
        }))
        return
      }

      if (adv2.status === 'awaiting_leg2_retry_sign') {
        const retryReq = adv2.signing_requests?.[0]
        if (!retryReq) throw new Error('Expected residual leg-2 retry signing request')
        setState((s) => ({
          ...s,
          phase: 'awaiting_leg2_retry',
          leg2Fill: adv2.leg2_fill ?? null,
          leg2Request: retryReq,
          mismatch: adv2.mismatch ?? null,
          reason: adv2.reason ?? null,
          expiresAt: retryReq.expires_at,
        }))

        const abortRetry = async (failureReason: string): Promise<AdvanceResp | null> => {
          const abortResp = await postAdvance(preparedAccounts, { session_id: prep.session_id, abort: true }).catch(() => null)
          if (!abortResp) {
            setState((s) => ({
              ...s,
              phase: 'degraded',
              reason: `${failureReason}; recovery request failed, manual action may be required`,
            }))
          }
          return abortResp
        }

        const changed = detectAccountChange()
        if (changed) {
          const abortResp = await abortRetry(`${changed} wallet changed before leg-2 retry`)
          if (!abortResp) return
          adv2 = abortResp
        } else {
          let signedRetry: SignedAction | null = null
          try {
            signedRetry = await tradingAgents.sign(retryReq)
          } catch (e) {
            const message = `Leg 2 retry signing failed: ${e instanceof Error ? e.message : 'unknown error'}`
            const abortResp = await abortRetry(message)
            if (!abortResp) return
            adv2 = { ...abortResp, reason: message }
          }
          if (signedRetry) {
            const changedAfterSign = detectAccountChange()
            if (changedAfterSign) {
              const abortResp = await abortRetry(`${changedAfterSign} wallet changed during leg-2 retry signing`)
              if (!abortResp) return
              adv2 = abortResp
            } else {
              setState((s) => ({ ...s, phase: 'submitting_leg2_retry' }))
              try {
                adv2 = await postAdvance(preparedAccounts, { session_id: prep.session_id, signed_actions: [signedRetry] })
              } catch (e) {
                setState((s) => ({
                  ...s,
                  phase: 'recovering',
                  reason: `Leg-2 retry response is uncertain: ${e instanceof Error ? e.message : 'unknown error'}`,
                }))
                return
              }
            }
          }
        }
      }

      setState((s) => ({
        ...s,
        phase: executionPhaseFromStatus(adv2.status),
        leg2Fill: adv2.leg2_fill ?? null,
        mismatch: adv2.mismatch ?? null,
        positionId: adv2.position_id ?? null,
        reason: adv2.reason ?? null,
        unwound: adv2.unwound ?? false,
        unwindStatus: (adv2.unwind_status ?? null) as UnwindStatus,
        remainingExposure: adv2.remaining_exposure ?? [],
      }))
    } catch (e) {
      setState((s) => ({
        ...s,
        phase: executionFailurePhase(exposurePossible),
        ...(exposurePossible
          ? { reason: `Execution response is uncertain: ${e instanceof Error ? e.message : 'Unknown error'}` }
          : { error: e instanceof Error ? e.message : 'Unknown error' }),
      }))
    }
  }, [asterAddress, pacificaAddress, hyperliquidAddress, tradingAgents])

  const reset = useCallback(() => setState(INITIAL_STATE), [])

  return { state, executeLive, reset }
}

type LiveExecutionContextValue = ReturnType<typeof useLiveExecutionState>

const LiveExecutionContext = createContext<LiveExecutionContextValue | null>(null)

export function LiveExecutionProvider({ children }: { children: ReactNode }) {
  const value = useLiveExecutionState()
  return createElement(LiveExecutionContext.Provider, { value }, children)
}

export function useLiveExecution(): LiveExecutionContextValue {
  const value = useContext(LiveExecutionContext)
  if (!value) throw new Error('useLiveExecution must be used within LiveExecutionProvider')
  return value
}
