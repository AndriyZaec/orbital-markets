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
}

export async function runAsterPrivateRequest<T>(
  input: AsterPrivateInput,
  sign: (request: SigningRequest) => Promise<SignedAction>,
  requestStillCurrent: () => boolean,
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
  if (request.venue !== 'aster' || request.action !== input.operation ||
    request.account.toLowerCase() !== input.account.toLowerCase() ||
    request.signer?.toLowerCase() !== input.agent.toLowerCase() ||
    request.symbol !== (input.symbol ?? '') ||
    request.client_order_id !== (input.client_order_id ?? '') ||
    (request.leverage ?? 0) !== (input.leverage ?? 0)) {
    throw new Error('Orbital returned a mismatched Aster signing request')
  }
  const signed = await sign(request)
  if (!requestStillCurrent()) {
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
  if (!requestStillCurrent()) {
    throw new Error('Aster owner or authorization changed during the private request')
  }
  return result.data
}
