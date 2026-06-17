import { useState, useCallback } from 'react'
import { apiFetch } from '@/lib/api'
import { useWallet } from '@solana/wallet-adapter-react'
import { useSignTypedData } from 'wagmi'
import { useVenueAuthority } from './useVenueAuthority'
import { signRequest, type Signers } from '@/lib/signing'
import type { SigningRequest, SignedAction } from '@/types/signing'

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

export type UnwindStatus = 'not_armed' | 'submit_failed' | 'unconfirmed' | 'confirmed' | null

export interface LiveExecutionState {
  phase: ExecutionPhase
  asset: string | null
  sessionId: string | null
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
}

const INITIAL_STATE: LiveExecutionState = {
  phase: 'idle',
  asset: null,
  sessionId: null,
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
}

interface PrepareResp {
  session_id: string
  asset: string
  riskier_venue: string
  hedge_venue: string
  expires_at: string
  signing_requests: SigningRequest[] // [leg1 open, leg1 unwind]
}

interface AdvanceResp {
  session_id: string
  status: 'awaiting_leg2_sign' | 'open' | 'degraded' | 'aborted' | 'failed'
  leg1_fill?: LegFillView
  leg2_fill?: LegFillView
  signing_requests?: SigningRequest[] // [leg2 open]
  mismatch?: number
  position_id?: string
  reason?: string
  unwound?: boolean
  unwind_status?: 'not_armed' | 'submit_failed' | 'unconfirmed' | 'confirmed'
}

export function useLiveExecution() {
  const [state, setState] = useState<LiveExecutionState>(INITIAL_STATE)
  const solWallet = useWallet()
  const { pacificaAddress, hyperliquidAddress } = useVenueAuthority()
  const { signTypedDataAsync } = useSignTypedData()

  const buildSigners = useCallback((): Signers => ({
    pacifica: solWallet.signMessage && solWallet.publicKey
      ? { signMessage: solWallet.signMessage, publicKey: solWallet.publicKey.toBase58() }
      : null,
    hyperliquid: hyperliquidAddress
      ? {
          signTypedDataAsync: (params) => signTypedDataAsync({
            domain: params.domain,
            types: params.types,
            primaryType: params.primaryType,
            message: params.message,
          }),
          address: hyperliquidAddress,
        }
      : null,
  }), [solWallet.signMessage, solWallet.publicKey, hyperliquidAddress, signTypedDataAsync])

  const postAdvance = async (body: Record<string, unknown>): Promise<AdvanceResp> => {
    const resp = await apiFetch('/api/v1/live/advance', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!resp.ok) {
      const b = await resp.json().catch(() => ({}))
      throw new Error(b.error || `Advance failed: HTTP ${resp.status}`)
    }
    return resp.json()
  }

  const executeLive = useCallback(async (opportunityId: string, leverage: number) => {
    if (!pacificaAddress || !hyperliquidAddress) {
      setState({ ...INITIAL_STATE, phase: 'failed', error: 'Both venue accounts must be connected' })
      return
    }

    setState({ ...INITIAL_STATE, phase: 'preparing' })

    try {
      // 1. Prepare — get session + leg-1 open & unwind signing requests.
      const prepResp = await apiFetch('/api/v1/live/prepare', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          opportunity_id: opportunityId,
          leverage,
          account_pacifica: pacificaAddress,
          account_hyperliquid: hyperliquidAddress,
        }),
      })
      if (!prepResp.ok) {
        const b = await prepResp.json().catch(() => ({}))
        throw new Error(b.error || `Prepare failed: HTTP ${prepResp.status}`)
      }
      const prep: PrepareResp = await prepResp.json()
      const leg1Requests = prep.signing_requests || []
      if (leg1Requests.length < 2) {
        throw new Error('Expected leg-1 open and unwind signing requests')
      }

      setState((s) => ({
        ...s,
        phase: 'awaiting_leg1',
        asset: prep.asset,
        sessionId: prep.session_id,
        riskierVenue: prep.riskier_venue,
        hedgeVenue: prep.hedge_venue,
        leg1Requests,
        expiresAt: prep.expires_at,
        currentVenue: prep.riskier_venue,
      }))

      const signers = buildSigners()

      // 2. Sign BOTH leg-1 requests up front. If either fails, submit nothing.
      let signedLeg1: SignedAction[]
      try {
        signedLeg1 = []
        for (const req of leg1Requests) {
          signedLeg1.push(await signRequest(req, signers))
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

      // 3. Advance step 1 — backend arms unwind, submits leg 1, waits for fill.
      setState((s) => ({ ...s, phase: 'submitting_leg1' }))
      const adv1 = await postAdvance({ session_id: prep.session_id, signed_actions: signedLeg1 })

      if (adv1.status === 'aborted' || adv1.status === 'failed' || adv1.status === 'degraded') {
        setState((s) => ({
          ...s,
          phase: adv1.status as ExecutionPhase,
          leg1Fill: adv1.leg1_fill ?? null,
          reason: adv1.reason ?? null,
          unwound: adv1.unwound ?? false,
          unwindStatus: (adv1.unwind_status ?? null) as UnwindStatus,
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

      // 4. Sign leg 2. If it fails, tell the backend to abort -> fires armed unwind.
      let signedLeg2: SignedAction
      try {
        signedLeg2 = await signRequest(leg2Req, signers)
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

      // 5. Advance step 2 — submit leg 2, verify hedge.
      setState((s) => ({ ...s, phase: 'submitting_leg2' }))
      const adv2 = await postAdvance({ session_id: prep.session_id, signed_actions: [signedLeg2] })

      setState((s) => ({
        ...s,
        phase: (adv2.status === 'open' ? 'open' : adv2.status) as ExecutionPhase,
        leg2Fill: adv2.leg2_fill ?? null,
        mismatch: adv2.mismatch ?? null,
        positionId: adv2.position_id ?? null,
        reason: adv2.reason ?? null,
        unwound: adv2.unwound ?? false,
        unwindStatus: (adv2.unwind_status ?? null) as UnwindStatus,
      }))
    } catch (e) {
      setState((s) => ({
        ...s,
        phase: 'failed',
        error: e instanceof Error ? e.message : 'Unknown error',
      }))
    }
  }, [pacificaAddress, hyperliquidAddress, buildSigners])

  const reset = useCallback(() => setState(INITIAL_STATE), [])

  return { state, executeLive, reset }
}
