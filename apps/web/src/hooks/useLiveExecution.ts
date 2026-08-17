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
import { useVenueAuthority } from './useVenueAuthority'
import type { SigningRequest, SignedAction } from '@/types/signing'
import { useTradingAgents } from './useTradingAgents'
import {
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
  signing_requests: SigningRequest[] // [two leverage updates, leg1 open, leg1 unwind]
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
  const { pacificaAddress, hyperliquidAddress } = useVenueAuthority()
  const tradingAgents = useTradingAgents()

  useEffect(() => {
    if (state.phase !== 'recovering' || !state.sessionId ||
      !state.accountPacifica || !state.accountHyperliquid) return

    let cancelled = false
    let streamConnected = false
    let fallbackTimer = 0
    let deadlineTimer = 0
    const sessionId = state.sessionId
    const query = new URLSearchParams({
      account_pacifica: state.accountPacifica,
      account_hyperliquid: state.accountHyperliquid,
    })
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
      state.accountPacifica,
      state.accountHyperliquid,
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
  }, [state.phase, state.sessionId, state.accountPacifica, state.accountHyperliquid])

  // Live refs of the currently connected accounts. The executeLive async
  // callback is created once and closes over stale addresses; refs let us
  // read the LATEST connected accounts inside the flow without re-creating
  // the callback (which would cancel in-flight sessions).
  const pacificaRef = useRef<string | null>(pacificaAddress)
  const hyperliquidRef = useRef<string | null>(hyperliquidAddress)
  useEffect(() => { pacificaRef.current = pacificaAddress }, [pacificaAddress])
  useEffect(() => { hyperliquidRef.current = hyperliquidAddress }, [hyperliquidAddress])

  const postAdvance = async (body: Record<string, unknown>): Promise<AdvanceResp> => {
    const resp = await apiFetch('/api/v1/live/advance', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
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
  ) => {
    if (!pacificaAddress || !hyperliquidAddress) {
      setState({ ...INITIAL_STATE, phase: 'failed', error: 'Both venue accounts must be connected' })
      return
    }
    if (tradingAgents.pacifica.status !== 'ready' || tradingAgents.hyperliquid.status !== 'ready' ||
      !tradingAgents.pacifica.agentAddress || !tradingAgents.hyperliquid.agentAddress) {
      setState({ ...INITIAL_STATE, phase: 'failed', error: 'Authorize both venues first' })
      return
    }

    setState({ ...INITIAL_STATE, phase: 'preparing' })

    let exposurePossible = false

    try {
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
          account_pacifica: pacificaAddress,
          account_hyperliquid: hyperliquidAddress,
          agent_pacifica: tradingAgents.pacifica.agentAddress,
          agent_hyperliquid: tradingAgents.hyperliquid.agentAddress,
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
      const leverageVenues = new Set(leg1Requests
        .filter((request) => request.action === 'update_leverage')
        .map((request) => request.venue))
      if (leg1Requests.length !== 4 || leverageVenues.size !== 2 ||
        !leg1Requests.some((request) => request.action === 'open') ||
        !leg1Requests.some((request) => request.action === 'close' && request.reduce_only)) {
        throw new Error('Expected two leverage updates plus leg-1 open and unwind requests')
      }

      // Snapshot the accounts the session was prepared for. Any subsequent
      // wallet change must halt the flow (before leg 1) or trigger abort +
      // armed unwind (after leg 1). Comparisons are normalized (lowercased
      // for EVM, trimmed for Solana) so casing/whitespace doesn't false-flag.
      const preparedPacifica = normalizePacificaAddress(pacificaAddress)
      const preparedHyperliquid = normalizeHyperliquidAddress(hyperliquidAddress)

      const detectAccountChange = (): string | null => {
        const nowPac = normalizePacificaAddress(pacificaRef.current)
        const nowHl = normalizeHyperliquidAddress(hyperliquidRef.current)
        if (nowPac !== preparedPacifica) return 'Pacifica'
        if (nowHl !== preparedHyperliquid) return 'Hyperliquid'
        return null
      }

      setState((s) => ({
        ...s,
        phase: 'awaiting_leg1',
        asset: prep.asset,
        sessionId: prep.session_id,
        accountPacifica: pacificaAddress,
        accountHyperliquid: hyperliquidAddress,
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

      // 2. Sign BOTH leg-1 requests up front. If either fails, submit nothing.
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
        adv1 = await postAdvance({ session_id: prep.session_id, signed_actions: signedLeg1 })
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
          const abortResp = await postAdvance({ session_id: prep.session_id, abort: true }).catch(() => null)
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
        const abortResp = await postAdvance({ session_id: prep.session_id, abort: true }).catch(() => null)
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
          const abortResp = await postAdvance({ session_id: prep.session_id, abort: true }).catch(() => null)
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
        adv2 = await postAdvance({ session_id: prep.session_id, signed_actions: [signedLeg2] })
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
          const abortResp = await postAdvance({ session_id: prep.session_id, abort: true }).catch(() => null)
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
                adv2 = await postAdvance({ session_id: prep.session_id, signed_actions: [signedRetry] })
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
  }, [pacificaAddress, hyperliquidAddress, tradingAgents])

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
