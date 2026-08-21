import { useState, useCallback } from 'react'
import { apiError, apiFetch, apiResponseError } from '@/lib/api'
import { useVenueAuthority } from './useVenueAuthority'
import type { SigningRequest, SignedAction, SubmissionResult } from '@/types/signing'
import { useTradingAgents } from './useTradingAgents'

export type ClosePhase = 'idle' | 'preparing' | 'signing' | 'submitting' | 'confirming' | 'done' | 'error'

export interface CloseOutcome {
  venue: SigningRequest['venue']
  symbol: string
  status: 'accepted' | 'failed' | 'uncertain'
  error?: string
}

export interface CloseState {
  phase: ClosePhase
  total: number
  submitted: number
  succeeded: number
  failed: number
  reconciled: boolean
  errors: string[]
  outcomes: CloseOutcome[]
}

const INITIAL: CloseState = {
  phase: 'idle',
  total: 0,
  submitted: 0,
  succeeded: 0,
  failed: 0,
  reconciled: false,
  errors: [],
  outcomes: [],
}

const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms))
const closeConfirmationAttempts = 12
const closeConfirmationPollMs = 2_000

async function waitForClose(positionId: string, pacificaAccount: string, hyperliquidAccount: string): Promise<void> {
	const query = new URLSearchParams({
		account_pacifica: pacificaAccount,
		account_hyperliquid: hyperliquidAccount,
	})
  for (let attempt = 0; attempt < closeConfirmationAttempts; attempt++) {
    const resp = await apiFetch(`/api/v1/live/positions/${positionId}?${query}`)
    if (!resp.ok) {
      throw await apiResponseError(resp, 'Unable to confirm the close. Check the position before retrying.')
    }
    const data: { position: { state: string } } = await resp.json()
    if (data.position.state === 'closed') return
    if (data.position.state === 'degraded') {
      throw new Error('A close fill was not confirmed; manual action may be required')
    }
    if (attempt < closeConfirmationAttempts - 1) {
      await delay(closeConfirmationPollMs)
    }
  }
  throw new Error('Close fill confirmation timed out; check the position before retrying')
}

export function useLiveClose() {
  const [state, setState] = useState<CloseState>(INITIAL)
  const { pacificaAddress, hyperliquidAddress } = useVenueAuthority()
  const tradingAgents = useTradingAgents()

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
          agent_pacifica: tradingAgents.pacifica.agentAddress,
          agent_hyperliquid: tradingAgents.hyperliquid.agentAddress,
        }),
      })
      if (!resp.ok) {
        const b = await resp.json().catch(() => ({}))
        throw apiError(resp.status, 'Unable to prepare the position close. Please try again.', b)
      }

      const data: { signing_requests: SigningRequest[]; reconciled_closed?: boolean } = await resp.json()
      const requests = data.signing_requests || []

      if (requests.length === 0) {
        setState(s => ({ ...s, phase: 'done', reconciled: data.reconciled_closed === true }))
        return
      }

      setState(s => ({ ...s, phase: 'signing', total: requests.length }))
      const errors: string[] = []
      const outcomes: CloseOutcome[] = []
      let failed = 0
      let succeeded = 0
      const signedActions: Array<{ request: SigningRequest; signed: SignedAction }> = []

      for (let i = 0; i < requests.length; i++) {
        const req = requests[i]
        setState(s => ({ ...s, phase: 'signing', submitted: i }))
        const signed = await tradingAgents.sign(req)
        signedActions.push({ request: req, signed })
      }

      setState(s => ({ ...s, phase: 'submitting', submitted: 0 }))
      for (let i = 0; i < signedActions.length; i++) {
        const { request: req, signed } = signedActions[i]
        try {
          const submitResp = await apiFetch('/api/v1/live/submit', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(signed),
          })
          if (!submitResp.ok) {
            const b = await submitResp.json().catch(() => ({}))
            failed++
            const message = apiError(submitResp.status, 'Order submission failed. Check the position before retrying.', b).message
            errors.push(`${req.venue} ${req.symbol}: ${message}`)
            outcomes.push({ venue: req.venue, symbol: req.symbol, status: 'failed', error: message })
            setState(s => ({ ...s, submitted: i + 1, succeeded, failed, errors: [...errors], outcomes: [...outcomes] }))
            continue
          }
          const result: SubmissionResult = await submitResp.json()

          if (!result.accepted && !result.uncertain) {
            failed++
            const message = result.error || 'rejected'
            errors.push(`${req.venue} ${req.symbol}: ${message}`)
            outcomes.push({ venue: req.venue, symbol: req.symbol, status: 'failed', error: message })
          } else if (result.accepted) {
            succeeded++
            outcomes.push({ venue: req.venue, symbol: req.symbol, status: 'accepted' })
          } else {
            outcomes.push({ venue: req.venue, symbol: req.symbol, status: 'uncertain' })
          }
          setState(s => ({ ...s, submitted: i + 1, succeeded, failed, errors: [...errors], outcomes: [...outcomes] }))
        } catch {
          errors.push(`${req.venue} ${req.symbol}: submission response uncertain; checking position state`)
          outcomes.push({ venue: req.venue, symbol: req.symbol, status: 'uncertain' })
          setState(s => ({ ...s, submitted: i + 1, succeeded, failed, errors: [...errors], outcomes: [...outcomes] }))
        }
      }

      if (failed > 0) {
        setState(s => ({ ...s, phase: 'done', succeeded, failed, errors: [...errors], outcomes: [...outcomes] }))
        return
      }

      setState(s => ({ ...s, phase: 'confirming' }))
      await waitForClose(positionId, pacificaAddress, hyperliquidAddress)
      setState(s => ({ ...s, phase: 'done', succeeded: requests.length }))
    } catch (e) {
      setState(s => ({
        ...s,
        phase: 'error',
        errors: [e instanceof Error ? e.message : 'Unknown error'],
      }))
    }
  }, [pacificaAddress, hyperliquidAddress, tradingAgents])

  const reset = useCallback(() => setState(INITIAL), [])

  return { state, closePosition, reset }
}
