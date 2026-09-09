import { apiFetch, apiResponseError } from '../lib/api.ts'
import type { SignedAction, SigningRequest } from '../types/signing.ts'

export type AsterPrivateOperation = Extract<SigningRequest['action'],
  | 'get_position_mode' | 'get_account' | 'get_positions' | 'get_leverage_brackets'
  | 'query_order' | 'update_leverage'
  | 'start_user_stream' | 'keepalive_user_stream' | 'close_user_stream'
>

export interface AsterPrivateInput {
  operation: AsterPrivateOperation
  account: string
  agent: string
  symbol?: string
  client_order_id?: string
  leverage?: number
}

interface AsterPrivateResponse<T> {
  request_id: string
  operation: AsterPrivateOperation
  data: T
  uncertain?: boolean
  error?: string
  state_applied?: boolean
  deposit_required?: boolean
}

interface AsterAccountSnapshotPayloads {
  snapshot_id: string
  requests: SigningRequest[]
}

export async function runAsterPrivateRequest<T>(
  input: AsterPrivateInput,
  sign: (request: SigningRequest) => Promise<SignedAction>,
  requestStillCurrent: () => boolean | Promise<boolean>,
): Promise<T> {
  const preparedResponse = await apiFetch('/api/v1/live/aster/private/prepare', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (!preparedResponse.ok) {
    throw await apiResponseError(preparedResponse, 'Unable to prepare the Aster request.')
  }
  const request = await preparedResponse.json() as SigningRequest
  const result = await submitPreparedAsterRequest<T>(input, request, sign, requestStillCurrent)
  return result.data
}

export async function refreshAsterAccountSnapshot(
  account: string,
  agent: string,
  sign: (request: SigningRequest) => Promise<SignedAction>,
  requestStillCurrent: () => boolean | Promise<boolean>,
	refreshOnly = false,
): Promise<'ready' | 'deposit_required'> {
  const preparedResponse = await apiFetch('/api/v1/live/aster/account/prepare', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ account, agent, ...(refreshOnly ? { refresh_only: true } : {}) }),
  })
  if (!preparedResponse.ok) {
    throw await apiResponseError(preparedResponse, 'Unable to prepare the Aster account snapshot.')
  }
  const payloads = await preparedResponse.json() as AsterAccountSnapshotPayloads
  const operations: AsterPrivateOperation[] = refreshOnly
    ? ['get_account', 'get_positions']
    : ['get_position_mode', 'get_account', 'get_positions']
  const requests = new Map(payloads.requests?.map((request) => [request.action, request]))
  const coherent = !!payloads.snapshot_id && payloads.requests?.length === operations.length &&
    requests.size === operations.length && operations.every((operation) => {
      const request = requests.get(operation)
      return request?.snapshot_id === payloads.snapshot_id && request.created_at === payloads.requests[0]?.created_at
    })
  if (!coherent) throw new Error('Orbital returned an incoherent Aster account snapshot')

  for (const operation of operations) {
    const request = requests.get(operation)!
    const result = await submitPreparedAsterRequest(
      { operation, account, agent }, request, sign, requestStillCurrent,
    )
    if (result.deposit_required) {
      if (!result.state_applied) throw new Error('Orbital did not apply the Aster unavailable state')
      return 'deposit_required'
    }
    if (!result.state_applied) {
      throw new Error('Orbital did not apply the Aster account snapshot')
    }
  }
  return 'ready'
}

async function submitPreparedAsterRequest<T>(
  input: AsterPrivateInput,
  request: SigningRequest,
  sign: (request: SigningRequest) => Promise<SignedAction>,
  requestStillCurrent: () => boolean | Promise<boolean>,
): Promise<AsterPrivateResponse<T>> {
  if (request.venue !== 'aster' || request.action !== input.operation ||
    request.account.toLowerCase() !== input.account.toLowerCase() ||
    request.signer?.toLowerCase() !== input.agent.toLowerCase() ||
    request.symbol !== (input.symbol ?? '') ||
    request.client_order_id !== (input.client_order_id ?? '') ||
    (request.leverage ?? 0) !== (input.leverage ?? 0)) {
    throw new Error('Orbital returned a mismatched Aster signing request')
  }
  const signed = await sign(request)
  if (!await requestStillCurrent()) {
    throw new Error('Aster owner or authorization changed during the private request')
  }
  const submitResponse = await apiFetch('/api/v1/live/aster/private/submit', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(signed),
  })
  if (!submitResponse.ok) {
    throw await apiResponseError(submitResponse, 'Unable to complete the Aster request.')
  }
  const result = await submitResponse.json() as AsterPrivateResponse<T>
  if (result.request_id !== request.id || result.operation !== input.operation) {
    throw new Error('Orbital returned a mismatched Aster response')
  }
  if (result.uncertain) {
    throw new Error(result.error || 'Aster request outcome is unknown; refresh account state before retrying')
  }
  if (!await requestStillCurrent()) {
    throw new Error('Aster owner or authorization changed during the private request')
  }
  return result
}
