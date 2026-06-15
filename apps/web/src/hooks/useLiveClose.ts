import { useState, useCallback } from 'react'
import { apiFetch } from '@/lib/api'
import { useWallet } from '@solana/wallet-adapter-react'
import { useSignTypedData } from 'wagmi'
import { useVenueAuthority } from './useVenueAuthority'
import { signRequest, type Signers } from '@/lib/signing'
import type { SigningRequest, SignedAction, SubmissionResult } from '@/types/signing'

export type ClosePhase = 'idle' | 'preparing' | 'signing' | 'submitting' | 'done' | 'error'

export interface CloseState {
  phase: ClosePhase
  total: number
  submitted: number
  succeeded: number
  failed: number
  errors: string[]
}

const INITIAL: CloseState = {
  phase: 'idle',
  total: 0,
  submitted: 0,
  succeeded: 0,
  failed: 0,
  errors: [],
}

export function useLiveClose() {
  const [state, setState] = useState<CloseState>(INITIAL)
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

  const closePosition = useCallback(async (positionId: string) => {
    if (!pacificaAddress || !hyperliquidAddress) {
      setState({ ...INITIAL, phase: 'error', errors: ['Both venue accounts must be connected'] })
      return
    }

    setState({ ...INITIAL, phase: 'preparing' })

    try {
      const resp = await apiFetch(`/api/v1/live/close/${positionId}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          account_pacifica: pacificaAddress,
          account_hyperliquid: hyperliquidAddress,
        }),
      })
      if (!resp.ok) {
        const b = await resp.json().catch(() => ({}))
        throw new Error(b.error || `Close prepare failed: HTTP ${resp.status}`)
      }

      const data: { signing_requests: SigningRequest[] } = await resp.json()
      const requests = data.signing_requests || []

      if (requests.length === 0) {
        setState(s => ({ ...s, phase: 'done' }))
        return
      }

      setState(s => ({ ...s, phase: 'signing', total: requests.length }))
      const signers = buildSigners()
      const errors: string[] = []
      let succeeded = 0
      let failed = 0

      for (let i = 0; i < requests.length; i++) {
        const req = requests[i]
        try {
          setState(s => ({ ...s, phase: 'signing', submitted: i }))
          const signed: SignedAction = await signRequest(req, signers)

          setState(s => ({ ...s, phase: 'submitting', submitted: i }))
          const submitResp = await apiFetch('/api/v1/live/submit', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(signed),
          })
          if (!submitResp.ok) {
            const b = await submitResp.json().catch(() => ({}))
            throw new Error(b.error || `HTTP ${submitResp.status}`)
          }
          const result: SubmissionResult = await submitResp.json()

          if (result.accepted) {
            succeeded++
          } else {
            failed++
            errors.push(`${req.venue} ${req.symbol}: ${result.error || 'rejected'}`)
          }
          setState(s => ({ ...s, submitted: i + 1, succeeded, failed, errors: [...errors] }))
        } catch (e) {
          failed++
          errors.push(`${req.venue} ${req.symbol}: ${e instanceof Error ? e.message : 'unknown'}`)
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

  return { state, closePosition, reset }
}
