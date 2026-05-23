import { useState, useCallback } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useSignTypedData } from 'wagmi'
import { useVenueAuthority } from './useVenueAuthority'
import { signRequest, type Signers } from '@/lib/signing'
import type {
  SigningRequest,
  SignedAction,
  SubmissionResult,
  PrepareResponse,
} from '@/types/signing'

export type ExecutionPhase =
  | 'idle'
  | 'preparing'
  | 'awaiting_signature_1'
  | 'awaiting_signature_2'
  | 'submitting_1'
  | 'submitting_2'
  | 'confirmed'
  | 'failed'

export interface LiveExecutionState {
  phase: ExecutionPhase
  signingRequests: SigningRequest[]
  results: SubmissionResult[]
  error: string | null
  failedVenue: string | null
  expiresAt: string | null
  currentVenue: string | null
  asset: string | null
}

const INITIAL_STATE: LiveExecutionState = {
  phase: 'idle',
  signingRequests: [],
  results: [],
  error: null,
  failedVenue: null,
  expiresAt: null,
  currentVenue: null,
  asset: null,
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

  const submitSigned = async (signed: SignedAction): Promise<SubmissionResult> => {
    const resp = await fetch('/api/v1/live/submit', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(signed),
    })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}))
      throw new Error(body.error || `Submit failed: HTTP ${resp.status}`)
    }
    return resp.json()
  }

  const executeLive = useCallback(async (opportunityId: string, leverage: number) => {
    if (!pacificaAddress || !hyperliquidAddress) {
      setState((s) => ({ ...s, phase: 'failed', error: 'Both venue accounts must be connected' }))
      return
    }

    setState({ ...INITIAL_STATE, phase: 'preparing' })

    try {
      const prepResp = await fetch('/api/v1/live/prepare', {
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
        const body = await prepResp.json().catch(() => ({}))
        throw new Error(body.error || `Prepare failed: HTTP ${prepResp.status}`)
      }

      const prepared: PrepareResponse = await prepResp.json()
      const requests = prepared.signing_requests

      if (requests.length < 2) {
        throw new Error('Expected 2 signing requests from backend')
      }

      const expiresAt = requests[0].expires_at
      setState((s) => ({
        ...s,
        phase: 'awaiting_signature_1',
        signingRequests: requests,
        expiresAt,
        currentVenue: requests[0].venue,
        asset: prepared.asset,
      }))

      const signers = buildSigners()

      // Sign & submit leg 1
      let signed1: SignedAction
      try {
        signed1 = await signRequest(requests[0], signers)
      } catch (e) {
        throw Object.assign(new Error(`Signing failed for ${requests[0].venue}: ${e instanceof Error ? e.message : 'Unknown error'}`), { venue: requests[0].venue })
      }

      setState((s) => ({ ...s, phase: 'submitting_1' }))
      const result1 = await submitSigned(signed1)
      if (!result1.accepted) {
        throw Object.assign(new Error(result1.error || `${requests[0].venue} leg rejected`), { venue: requests[0].venue })
      }

      // Sign & submit leg 2
      setState((s) => ({
        ...s,
        phase: 'awaiting_signature_2',
        currentVenue: requests[1].venue,
        results: [result1],
      }))

      let signed2: SignedAction
      try {
        signed2 = await signRequest(requests[1], signers)
      } catch (e) {
        throw Object.assign(new Error(`Signing failed for ${requests[1].venue}: ${e instanceof Error ? e.message : 'Unknown error'}`), { venue: requests[1].venue })
      }

      setState((s) => ({ ...s, phase: 'submitting_2' }))
      const result2 = await submitSigned(signed2)
      if (!result2.accepted) {
        throw Object.assign(new Error(result2.error || `${requests[1].venue} leg rejected`), { venue: requests[1].venue })
      }

      setState((s) => ({
        ...s,
        phase: 'confirmed',
        results: [result1, result2],
        currentVenue: null,
      }))
    } catch (e) {
      const err = e as Error & { venue?: string }
      setState((s) => ({
        ...s,
        phase: 'failed',
        error: err.message,
        failedVenue: err.venue ?? null,
        currentVenue: null,
      }))
    }
  }, [pacificaAddress, hyperliquidAddress, buildSigners])

  const reset = useCallback(() => setState(INITIAL_STATE), [])

  return { state, executeLive, reset }
}
