import { useState, useCallback } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useSignTypedData } from 'wagmi'
import { useVenueAuthority } from './useVenueAuthority'
import { signRequest, type Signers } from '@/lib/signing'
import type { SigningRequest, SignedAction, SubmissionResult } from '@/types/signing'

export type KillPhase =
  | 'idle'
  | 'preparing'
  | 'signing'
  | 'submitting'
  | 'done'
  | 'error'

export interface KillPositionInfo {
  id: string
  asset: string
  state: string
  legs_to_close: number
  error?: string
}

export interface KillState {
  phase: KillPhase
  targeted: number
  totalRequests: number
  signed: number
  submitted: number
  succeeded: number
  failed: number
  positions: KillPositionInfo[]
  errors: string[]
}

const INITIAL: KillState = {
  phase: 'idle',
  targeted: 0,
  totalRequests: 0,
  signed: 0,
  submitted: 0,
  succeeded: 0,
  failed: 0,
  positions: [],
  errors: [],
}

export function useKillSwitch() {
  const [state, setState] = useState<KillState>(INITIAL)
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

  const execute = useCallback(async () => {
    if (!pacificaAddress || !hyperliquidAddress) {
      setState(s => ({ ...s, phase: 'error', errors: ['Both venue accounts must be connected'] }))
      return
    }

    setState({ ...INITIAL, phase: 'preparing' })

    try {
      // 1. Get close signing requests from backend
      const resp = await fetch('/api/v1/live/kill', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          account_pacifica: pacificaAddress,
          account_hyperliquid: hyperliquidAddress,
        }),
      })
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}))
        throw new Error(body.error || `Kill prepare failed: HTTP ${resp.status}`)
      }

      const data: {
        targeted: number
        signing_requests: SigningRequest[]
        positions: KillPositionInfo[]
      } = await resp.json()

      if (data.targeted === 0) {
        setState(s => ({ ...s, phase: 'done', targeted: 0, positions: [] }))
        return
      }

      const requests = data.signing_requests || []
      setState(s => ({
        ...s,
        phase: 'signing',
        targeted: data.targeted,
        totalRequests: requests.length,
        positions: data.positions,
      }))

      // 2. Sign and submit each close order
      const signers = buildSigners()
      const errors: string[] = []
      let succeeded = 0
      let failed = 0

      for (let i = 0; i < requests.length; i++) {
        const req = requests[i]

        try {
          // Sign
          setState(s => ({ ...s, phase: 'signing', signed: i }))
          const signed = await signRequest(req, signers)

          // Submit
          setState(s => ({ ...s, phase: 'submitting', signed: i + 1, submitted: i }))
          const result = await submitSigned(signed)

          if (result.accepted) {
            succeeded++
          } else {
            failed++
            errors.push(`${req.venue} ${req.symbol}: ${result.error || 'rejected'}`)
          }

          setState(s => ({ ...s, submitted: i + 1, succeeded, failed, errors: [...errors] }))
        } catch (e) {
          failed++
          const msg = `${req.venue} ${req.symbol}: ${e instanceof Error ? e.message : 'unknown error'}`
          errors.push(msg)
          setState(s => ({ ...s, submitted: i + 1, failed, errors: [...errors] }))
        }
      }

      setState(s => ({ ...s, phase: 'done', succeeded, failed, errors: [...errors] }))
    } catch (e) {
      setState(s => ({
        ...s,
        phase: 'error',
        errors: [e instanceof Error ? e.message : 'Unknown error'],
      }))
    }
  }, [pacificaAddress, hyperliquidAddress, buildSigners])

  const reset = useCallback(() => setState(INITIAL), [])

  return { state, execute, reset }
}
