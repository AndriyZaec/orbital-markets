// Bounds read-only account work, including response bodies. Never wrap signing
// or submission: their uncertain outcomes have a separate recovery lifecycle.
export async function accountRead<T>(read: (signal: AbortSignal) => Promise<T>, parent?: AbortSignal, timeoutMs = 10_000): Promise<T> {
  const controller = new AbortController()
  let rejectAbort: (reason: Error) => void = () => {}
  const aborted = new Promise<never>((_, reject) => { rejectAbort = reject })
  const abort = () => {
    controller.abort()
    rejectAbort(new DOMException('Account read cancelled', 'AbortError'))
  }
  parent?.addEventListener('abort', abort, { once: true })
  const timer = setTimeout(() => {
    controller.abort()
    rejectAbort(new Error('Account refresh timed out. Retrying.'))
  }, timeoutMs)
  try {
    if (parent?.aborted) abort()
    return await Promise.race([aborted, parent?.aborted ? aborted : read(controller.signal)])
  } finally {
    clearTimeout(timer)
    parent?.removeEventListener('abort', abort)
  }
}
