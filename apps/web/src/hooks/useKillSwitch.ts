import { useState, useCallback } from 'react'
import { apiError, apiFetch } from '@/lib/api'
import { useVenueAuthority } from './useVenueAuthority'
import type { SigningRequest, SignedAction, SubmissionResult } from '@/types/signing'
import { useTradingAgents } from './useTradingAgents'
import { summarizeKillPreparation } from '@/lib/kill-switch'

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
  remaining_exposure: Array<{
    leg: number
    venue: string
    symbol: string
    side: string
    amount: number
  }>
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
  uncertain: number
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
  uncertain: 0,
  positions: [],
  errors: [],
}

export function useKillSwitch() {
  const [state, setState] = useState<KillState>(INITIAL)
  const { pacificaAddress, hyperliquidAddress } = useVenueAuthority()
  const tradingAgents = useTradingAgents()

  const submitSigned = async (signed: SignedAction): Promise<SubmissionResult> => {
    const resp = await apiFetch('/api/v1/live/submit', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(signed),
    })
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}))
      return {
        request_id: signed.request_id,
        client_order_id: signed.client_order_id,
        venue: signed.venue,
        accepted: false,
        error: apiError(resp.status, 'Order submission failed. Check the position state.', body).message,
        submitted_at: '',
        responded_at: '',
      }
    }
    return resp.json()
  }

  const execute = useCallback(async () => {
    if (!pacificaAddress || !hyperliquidAddress) {
      setState(s => ({ ...s, phase: 'error', errors: ['Both venue accounts must be connected'] }))
      return
    }
    if (!tradingAgents.pacifica.agentAddress || !tradingAgents.hyperliquid.agentAddress ||
      tradingAgents.pacifica.status !== 'ready' || tradingAgents.hyperliquid.status !== 'ready') {
      setState(s => ({ ...s, phase: 'error', errors: ['Authorize both venues first'] }))
      return
    }

    setState({ ...INITIAL, phase: 'preparing' })

    try {
      // 1. Get close signing requests from backend
      const resp = await apiFetch('/api/v1/live/kill', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          account_pacifica: pacificaAddress,
          account_hyperliquid: hyperliquidAddress,
          agent_pacifica: tradingAgents.pacifica.agentAddress,
          agent_hyperliquid: tradingAgents.hyperliquid.agentAddress,
        }),
      })
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}))
        throw apiError(resp.status, 'Unable to prepare emergency close. Please try again.', body)
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
      const preparation = summarizeKillPreparation(data.targeted, requests.length, data.positions)
      if (requests.length === 0) {
        setState(s => ({
          ...s,
          phase: 'error',
          targeted: data.targeted,
          positions: data.positions,
          failed: preparation.failed,
          errors: preparation.errors,
        }))
        return
      }
      setState(s => ({
        ...s,
        phase: 'signing',
        targeted: data.targeted,
        totalRequests: requests.length,
        positions: data.positions,
        failed: preparation.failed,
        errors: preparation.errors,
      }))

      // Sign every reduce-only action before submitting any venue order.
      const errors: string[] = [...preparation.errors]
      let succeeded = 0
      let failed = preparation.failed
      let uncertain = 0
      const signedActions: Array<{ request: SigningRequest; signed: SignedAction }> = []
      for (let i = 0; i < requests.length; i++) {
        const req = requests[i]
        setState(s => ({ ...s, phase: 'signing', signed: i }))
        signedActions.push({ request: req, signed: await tradingAgents.sign(req) })
      }

      for (let i = 0; i < signedActions.length; i++) {
        const { request: req, signed } = signedActions[i]
        try {
          setState(s => ({ ...s, phase: 'submitting', signed: signedActions.length, submitted: i }))
          const result = await submitSigned(signed)

          if (result.accepted) {
            succeeded++
          } else if (result.uncertain) {
            uncertain++
          } else {
            failed++
            errors.push(`${req.venue} ${req.symbol}: ${result.error || 'rejected'}`)
          }

          setState(s => ({ ...s, submitted: i + 1, succeeded, failed, uncertain, errors: [...errors] }))
        } catch {
          uncertain++
          setState(s => ({ ...s, submitted: i + 1, uncertain }))
        }
      }

      setState(s => ({ ...s, phase: 'done', succeeded, failed, uncertain, errors: [...errors] }))
    } catch (e) {
      setState(s => ({
        ...s,
        phase: 'error',
        errors: [e instanceof Error ? e.message : 'Unknown error'],
      }))
    }
  }, [pacificaAddress, hyperliquidAddress, tradingAgents])

  const reset = useCallback(() => setState(INITIAL), [])

  return { state, execute, reset }
}
