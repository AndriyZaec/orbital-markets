import { useState, useCallback } from 'react'
import { apiError, apiFetch, apiResponseError } from '@/lib/api'
import { useVenueAuthority } from './useVenueAuthority'
import type { SigningRequest, SignedAction, SubmissionResult } from '@/types/signing'
import { useTradingAgents } from './useTradingAgents'
import { waitForClosedPosition } from '@/lib/live-close'
import { liveAccountsQuery, liveVenueBindingsBody } from '@/lib/live-bindings'
import { submitSignedActionsConcurrently } from '@/lib/signed-submissions'
import type { Venue } from '@/agents/types'

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

const delay = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))
const closeConfirmationAttempts = 12
const closeConfirmationPollMs = 2_000

async function waitForClose(positionId: string, accounts: Record<string, string>): Promise<void> {
  const query = liveAccountsQuery(accounts)
  await waitForClosedPosition({
    getPositionState: async () => {
      const resp = await apiFetch(`/api/v1/live/positions/${positionId}?${query}`)
      if (!resp.ok) {
        throw await apiResponseError(resp, 'Unable to confirm the close. Check the position before retrying.')
      }
      const data: { position: { state: string } } = await resp.json()
      return data.position.state
    },
    delay,
    attempts: closeConfirmationAttempts,
    pollMs: closeConfirmationPollMs,
  })
}

export function useLiveClose() {
  const [state, setState] = useState<CloseState>(INITIAL)
  const { pacificaAddress, hyperliquidAddress, asterAddress } = useVenueAuthority()
  const tradingAgents = useTradingAgents()

  const closePosition = useCallback(async (
    positionId: string,
    venues: [Venue, Venue] = ['pacifica', 'hyperliquid'],
  ) => {
    const authorityByVenue: Record<Venue, string | null> = {
      pacifica: pacificaAddress, hyperliquid: hyperliquidAddress, aster: asterAddress,
    }
    const agentByVenue = {
      pacifica: tradingAgents.pacifica,
      hyperliquid: tradingAgents.hyperliquid,
      aster: tradingAgents.aster,
    }
    if (venues.some((venue) => !authorityByVenue[venue])) {
      setState({ ...INITIAL, phase: 'error', errors: ['Both venue accounts must be connected'] })
      return
    }
    const accounts = Object.fromEntries(venues.map((venue) => [venue, authorityByVenue[venue]!]))
    const agents = Object.fromEntries(venues.map((venue) => [venue, agentByVenue[venue].agentAddress ?? '']))
    setState({ ...INITIAL, phase: 'preparing' })

    try {
      const resp = await apiFetch(`/api/v1/live/close/${positionId}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(liveVenueBindingsBody(
          accounts,
          agents,
        )),
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
      const submittedActions = await submitSignedActionsConcurrently(signedActions, async (signed) => {
        const submitResp = await apiFetch('/api/v1/live/submit', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(signed),
        })
        if (!submitResp.ok) {
          const b = await submitResp.json().catch(() => ({}))
          return {
            request_id: signed.request_id,
            client_order_id: signed.client_order_id,
            venue: signed.venue,
            accepted: false,
            error: apiError(submitResp.status, 'Order submission failed. Check the position before retrying.', b).message,
            submitted_at: '',
            responded_at: '',
          }
        }
        return submitResp.json() as Promise<SubmissionResult>
      })

      for (const { request: req, outcome } of submittedActions) {
        if (outcome.status === 'rejected') {
          errors.push(`${req.venue} ${req.symbol}: submission response uncertain; checking position state`)
          outcomes.push({ venue: req.venue, symbol: req.symbol, status: 'uncertain' })
        } else {
          const result = outcome.value
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
        }
      }
      setState(s => ({
        ...s,
        submitted: submittedActions.length,
        succeeded,
        failed,
        errors: [...errors],
        outcomes: [...outcomes],
      }))

      if (failed > 0) {
        setState(s => ({ ...s, phase: 'done', succeeded, failed, errors: [...errors], outcomes: [...outcomes] }))
        return
      }

      setState(s => ({ ...s, phase: 'confirming' }))
      await waitForClose(positionId, accounts)
      setState(s => ({ ...s, phase: 'done', succeeded: requests.length }))
    } catch (e) {
      setState(s => ({
        ...s,
        phase: 'error',
        errors: [e instanceof Error ? e.message : 'Unknown error'],
      }))
    }
  }, [asterAddress, pacificaAddress, hyperliquidAddress, tradingAgents])

  const reset = useCallback(() => setState(INITIAL), [])

  return { state, closePosition, reset }
}
