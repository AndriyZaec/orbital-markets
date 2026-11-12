import { apiError, apiFetch } from './api.ts'

export const PREPARE_STAGE_LABELS = {
  validating: 'Checking execution request',
  quotes: 'Checking fresh quotes and size',
  waiting_bbo: 'Waiting for fresh market quotes',
  accounts: 'Checking account readiness and balances',
  liquidity: 'Verifying executable liquidity',
  signing_requests: 'Preparing signing requests',
} as const

export type PrepareStage = keyof typeof PREPARE_STAGE_LABELS
export const PREPARE_TIMEOUT_MS = 20_000

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object'
}

export class PrepareError extends Error {}

const PREPARE_FEEDBACK: Record<string, string> = {
  ACCOUNT_STATE_NOT_READY: 'Accounts are updating.',
  POSITION_STATE_NOT_READY: 'Accounts are updating.',
  EXECUTION_DATA_NOT_READY: 'Market data is updating.',
  PREPARE_TIMEOUT: 'Preparation timed out.',
  LIVE_ADMISSION_DENIED: 'Trade unavailable.',
  PLAN_NOT_EXECUTABLE: 'Trade unavailable.',
  PRETRADE_BLOCKED: 'Trade unavailable.',
  EXISTING_POSITION: 'Close the existing position first.',
  EXISTING_SESSION: 'A trade is already in progress.',
  PRICE_MOVED: 'Prices changed. Review the trade.',
}

function resultBody<T>(status: number, body: unknown): T {
  if (status >= 200 && status < 300) {
    if (!isRecord(body)) throw new PrepareError('Unable to start trade.')
    return body as T
  }
  const code = isRecord(body) && typeof body.code === 'string' ? body.code : ''
  // Adapter diagnostics remain on the backend. Only structured, allowlisted
  // feedback is UI copy; an unknown code falls back without exposing reasons.
  throw new PrepareError(Object.hasOwn(PREPARE_FEEDBACK, code) ? PREPARE_FEEDBACK[code]
    : apiError(status, 'Unable to start trade.').message)
}

// One request, no progress polling. The deadline covers headers AND the body,
// and applies only before signing/submission, never to an exposed trade.
export async function prepareLive<T>(
  body: Record<string, unknown>,
  onProgress: (stage: PrepareStage) => void,
  timeoutMs = PREPARE_TIMEOUT_MS,
): Promise<T> {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      reject(new PrepareError('Preparation timed out.'))
      controller.abort()
    }, timeoutMs)
  })

  const request = async (): Promise<T> => {
    const response = await apiFetch('/api/v1/live/prepare', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/x-ndjson' },
      signal: controller.signal,
      body: JSON.stringify(body),
    })
    // Compatibility with an older API or a non-streaming middleware error.
    if (!response.headers.get('Content-Type')?.includes('application/x-ndjson')) {
      const payload: unknown = await response.json().catch(() => null)
      return resultBody<T>(response.status, payload)
    }
    if (!response.body) throw new PrepareError('Unable to start trade.')
    const reader = response.body.getReader()
    const cancelReader = () => { void reader.cancel().catch(() => {}) }
    controller.signal.addEventListener('abort', cancelReader, { once: true })
    const decoder = new TextDecoder()
    let pending = ''
    try {
      while (true) {
        controller.signal.throwIfAborted()
        const { value, done } = await reader.read()
        controller.signal.throwIfAborted()
        pending += decoder.decode(value, { stream: !done })
        const lines = pending.split('\n')
        pending = lines.pop() ?? ''
        if (done && pending.trim()) lines.push(pending)
        for (const line of lines) {
          if (!line.trim()) continue
          const event: unknown = JSON.parse(line)
          if (!isRecord(event)) throw new PrepareError('Unable to start trade.')
          if (event.type === 'progress' && typeof event.stage === 'string' && Object.hasOwn(PREPARE_STAGE_LABELS, event.stage)) {
            onProgress(event.stage as PrepareStage)
          } else if (event.type === 'result' && typeof event.status === 'number' && Number.isInteger(event.status) && event.status >= 200 && event.status < 600 && 'body' in event) {
            return resultBody<T>(event.status, event.body)
          }
        }
        if (done) throw new PrepareError('Unable to start trade.')
      }
    } finally {
      controller.signal.removeEventListener('abort', cancelReader)
      await reader.cancel().catch(() => {})
      reader.releaseLock()
    }
  }
  try {
    return await Promise.race([request(), timeout])
  } catch (cause) {
    if (cause instanceof PrepareError) throw cause
    throw new PrepareError(cause instanceof TypeError ? 'Check your connection.' : 'Unable to start trade.')
  } finally {
    clearTimeout(timer)
  }
}
