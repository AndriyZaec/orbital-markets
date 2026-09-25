import { useState, useCallback } from 'react'
import { apiError, apiFetch, apiResponseError } from '@/lib/api'
import { useVenueAuthority } from './useVenueAuthority'
import type { SigningRequest, SignedAction, SubmissionResult } from '@/types/signing'
import { useTradingAgents } from './useTradingAgents'
import { summarizeKillPreparation, waitForKilledPositions } from '@/lib/kill-switch'
import { liveAccountsQuery, liveVenueBindingsBody } from '@/lib/live-bindings'
import { submitSignedActionsConcurrently } from '@/lib/signed-submissions'
import type { Venue } from '@/agents/types'

export type KillPhase =
  | 'idle'
  | 'preparing'
  | 'signing'
  | 'submitting'
  | 'confirming'
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

const delay = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))
const killConfirmationAttempts = 12
const killConfirmationPollMs = 2_000

export function useKillSwitch() {
  const [state, setState] = useState<KillState>(INITIAL)
  const { pacificaAddress, hyperliquidAddress, asterAddress } = useVenueAuthority()
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
    const connected = [
      { venue: 'pacifica' as Venue, account: pacificaAddress, agent: tradingAgents.pacifica },
      { venue: 'hyperliquid' as Venue, account: hyperliquidAddress, agent: tradingAgents.hyperliquid },
      { venue: 'aster' as Venue, account: asterAddress, agent: tradingAgents.aster },
    ].filter((entry) => entry.account)
    const connectedPairs: Array<typeof connected> = []
    for (let left = 0; left < connected.length; left++) {
      for (let right = left + 1; right < connected.length; right++) {
        connectedPairs.push([connected[left], connected[right]])
      }
    }
    if (connectedPairs.length === 0) {
      setState(s => ({ ...s, phase: 'error', errors: ['Connect at least two live venues first'] }))
      return
    }

    setState({ ...INITIAL, phase: 'preparing' })

    try {
      const pairKey = (pair: typeof connected) => pair.map((entry) => entry.venue).sort().join(':')
      const discovery = await Promise.allSettled(connectedPairs.map(async (pair) => {
        const accounts = Object.fromEntries(pair.map((entry) => [entry.venue, entry.account!]))
        const response = await apiFetch(`/api/v1/live/positions?${liveAccountsQuery(accounts)}`)
        if (!response.ok) {
          throw await apiResponseError(response, 'Unable to verify all live positions before emergency close.')
        }
        const positions = await response.json() as Array<{ id: string; asset: string; venue_a: Venue; venue_b: Venue; state: string }>
        return { pair, positions }
      }))
      const visiblePositions = discovery
        .filter((result): result is PromiseFulfilledResult<{
          pair: typeof connected
          positions: Array<{ id: string; asset: string; venue_a: Venue; venue_b: Venue; state: string }>
        }> => result.status === 'fulfilled')
        .flatMap((result) => result.value.positions)
      const closeable = [...new Map(visiblePositions
        .filter((position) => ['open', 'degraded', 'closing'].includes(position.state))
        .map((position) => [position.id, position])).values()]
      const omittedPositions: KillPositionInfo[] = closeable.filter((position) =>
        [position.venue_a, position.venue_b].some((venue) => {
          const connection = connected.find((entry) => entry.venue === venue)
          return !connection || connection.agent.status !== 'ready' || !connection.agent.agentAddress
        })).map((position) => ({
          ...position,
          legs_to_close: 0,
          remaining_exposure: [],
          error: `Authorize ${position.venue_a} and ${position.venue_b} before emergency close`,
        }))
      const discoveryFailures = discovery.flatMap((result, index) => result.status === 'fulfilled' ? [] : [{
        key: pairKey(connectedPairs[index]),
        message: `Could not verify positions for ${connectedPairs[index].map((entry) => entry.venue).join(' / ')}`,
      }])
      const pairs = connectedPairs.filter((pair) => pair.every((entry) =>
        entry.agent.status === 'ready' && entry.agent.agentAddress))
      if (pairs.length === 0) {
        const errors = [...omittedPositions.map((position) => position.error!), ...discoveryFailures.map((failure) => failure.message)]
        setState(s => ({
          ...s,
          phase: errors.length > 0 ? 'error' : 'done',
          targeted: omittedPositions.length,
          failed: omittedPositions.length,
          positions: omittedPositions,
          errors,
        }))
        return
      }
      const positionAccounts = new Map<string, Record<string, string>>()
      const preparedResults = await Promise.allSettled(pairs.map(async (pair) => {
        const accounts = Object.fromEntries(pair.map((entry) => [entry.venue, entry.account!]))
        const agents = Object.fromEntries(pair.map((entry) => [entry.venue, entry.agent.agentAddress!]))
        const resp = await apiFetch('/api/v1/live/kill', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(liveVenueBindingsBody(accounts, agents)),
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
        for (const position of data.positions) positionAccounts.set(position.id, accounts)
        return data
      }))
      const readyPairKeys = new Set(pairs.map(pairKey))
      const failedPreparationKeys = new Set(preparedResults.flatMap((result, index) =>
        result.status === 'rejected' ? [pairKey(pairs[index])] : []))
      const unresolvedDiscoveryErrors = discoveryFailures
        .filter((failure) => !readyPairKeys.has(failure.key) || failedPreparationKeys.has(failure.key))
        .map((failure) => failure.message)
      const failedPositions: KillPositionInfo[] = [...omittedPositions]
      preparedResults.forEach((result, index) => {
        if (result.status === 'fulfilled') return
        const failedVenues = new Set(pairs[index].map((entry) => entry.venue))
        for (const position of closeable) {
          if (failedVenues.has(position.venue_a) && failedVenues.has(position.venue_b)) {
            failedPositions.push({
              ...position,
              legs_to_close: 0,
              remaining_exposure: [],
              error: result.reason instanceof Error ? result.reason.message : 'Emergency close preparation failed',
            })
          }
        }
      })
      const prepared = preparedResults
        .filter((result): result is PromiseFulfilledResult<{
          targeted: number
          signing_requests: SigningRequest[]
          positions: KillPositionInfo[]
        }> => result.status === 'fulfilled')
        .map((result) => result.value)
      const data = {
        targeted: prepared.reduce((total, result) => total + result.targeted, 0) + failedPositions.length,
        signing_requests: prepared.flatMap((result) => result.signing_requests),
        positions: [...prepared.flatMap((result) => result.positions), ...failedPositions],
      }

      if (data.targeted === 0) {
        setState(s => ({
          ...s,
          phase: unresolvedDiscoveryErrors.length > 0 ? 'error' : 'done',
          targeted: 0,
          positions: [],
          errors: unresolvedDiscoveryErrors,
        }))
        return
      }

      const requests = data.signing_requests || []
      const preparation = summarizeKillPreparation(data.targeted, requests.length, data.positions)
      preparation.errors.push(...unresolvedDiscoveryErrors)
      const positionIds = [...new Set(data.positions.map(position => position.id).filter(Boolean))]
      if (positionIds.length !== data.targeted) {
        setState(s => ({
          ...s,
          phase: 'error',
          targeted: data.targeted,
          positions: data.positions,
          failed: data.targeted,
          errors: ['Emergency close confirmation targets were incomplete'],
        }))
        return
      }
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

      setState(s => ({ ...s, phase: 'submitting', signed: signedActions.length, submitted: 0 }))
      const submittedActions = await submitSignedActionsConcurrently(signedActions, submitSigned)
      for (const { request: req, outcome } of submittedActions) {
        if (outcome.status === 'rejected') {
          uncertain++
        } else {
          const result = outcome.value
          if (result.accepted) {
            succeeded++
          } else if (result.uncertain) {
            uncertain++
          } else {
            failed++
            errors.push(`${req.venue} ${req.symbol}: ${result.error || 'rejected'}`)
          }
        }
      }
      setState(s => ({
        ...s,
        submitted: submittedActions.length,
        succeeded,
        failed,
        uncertain,
        errors: [...errors],
      }))

      if (failed > 0 || unresolvedDiscoveryErrors.length > 0) {
        setState(s => ({ ...s, phase: 'error', succeeded, failed, uncertain, errors: [...errors] }))
        return
      }

      setState(s => ({ ...s, phase: 'confirming', succeeded, failed, uncertain, errors: [...errors] }))
      await waitForKilledPositions({
        positionIds,
        getPositionState: async (positionId) => {
          const accounts = positionAccounts.get(positionId)
          if (!accounts) throw new Error('Missing account bindings for emergency close confirmation')
          const query = liveAccountsQuery(accounts)
          const positionResp = await apiFetch(`/api/v1/live/positions/${positionId}?${query}`)
          if (!positionResp.ok) {
            throw await apiResponseError(positionResp, 'Unable to confirm emergency close. Check all venue positions.')
          }
          const positionData: { position: { state: string } } = await positionResp.json()
          return positionData.position.state
        },
        delay,
        attempts: killConfirmationAttempts,
        pollMs: killConfirmationPollMs,
      })
      setState(s => ({ ...s, phase: 'done', succeeded, failed, uncertain, errors: [...errors] }))
    } catch (e) {
      setState(s => ({
        ...s,
        phase: 'error',
        errors: [e instanceof Error ? e.message : 'Unknown error'],
      }))
    }
  }, [asterAddress, pacificaAddress, hyperliquidAddress, tradingAgents])

  const reset = useCallback(() => setState(INITIAL), [])

  return { state, execute, reset }
}
