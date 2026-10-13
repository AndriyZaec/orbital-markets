import { apiError, apiFetch, userErrorMessage } from './api.ts'

export const PREPARE_STAGE_LABELS = {
  validating: 'Checking execution request',
  quotes: 'Checking fresh quotes and size',
  accounts: 'Checking account readiness and balances',
  liquidity: 'Verifying executable liquidity',
  signing_requests: 'Preparing signing requests',
} as const

export type PrepareStage = keyof typeof PREPARE_STAGE_LABELS
export const PREPARE_TIMEOUT_MS = 20_000

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object'
}

function resultBody<T>(status: number, body: unknown): T {
  if (status >= 200 && status < 300) {
    if (!isRecord(body)) throw new Error('Invalid preparation response. Please try again.')
    return body as T
  }
  const reasons = isRecord(body) && Array.isArray(body.reasons)
    ? body.reasons.filter((reason): reason is string => typeof reason === 'string')
    : []
  const message = apiError(status, 'Unable to prepare execution. Refresh the opportunity and try again.', body).message
  throw new Error(reasons.length > 0 ? `${message}: ${reasons.join('; ')}` : message)
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
      reject(new Error('Preparation timed out. No orders were submitted. Refresh the opportunity and try again.'))
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
    if (!response.body) throw new Error('Preparation response was empty. Please try again.')
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
          if (!isRecord(event)) throw new Error('Invalid preparation response. Please try again.')
          if (event.type === 'progress' && typeof event.stage === 'string' && Object.hasOwn(PREPARE_STAGE_LABELS, event.stage)) {
            onProgress(event.stage as PrepareStage)
          } else if (event.type === 'result' && typeof event.status === 'number' && Number.isInteger(event.status) && event.status >= 200 && event.status < 600 && 'body' in event) {
            return resultBody<T>(event.status, event.body)
          }
        }
        if (done) throw new Error('Preparation connection ended before completion. No orders were submitted. Please try again.')
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
    throw new Error(userErrorMessage(cause, 'Unable to prepare execution. Refresh the opportunity and try again.'))
  } finally {
    clearTimeout(timer)
  }
}
