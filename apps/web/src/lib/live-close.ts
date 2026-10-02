export interface WaitForCloseOptions {
  getPositionState: () => Promise<string>
  delay: (ms: number) => Promise<void>
  attempts: number
  pollMs: number
}

export async function withClosePreparationTimeout<T>(
  request: (signal: AbortSignal) => Promise<T>,
  timeoutMs = 10_000,
): Promise<T> {
  const controller = new AbortController()
  let timedOut = false
  const timer = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, timeoutMs)
  try {
    return await request(controller.signal)
  } catch (error) {
    if (timedOut || (error instanceof DOMException && error.name === 'AbortError')) {
      throw new Error('Close preparation took too long. No close order was submitted. Try again.')
    }
    if (error instanceof TypeError && /failed to fetch|load failed|network(?:error| request)/i.test(error.message)) {
      throw new Error('Unable to reach Orbital while preparing the close. No close order was submitted. Try again.')
    }
    throw error
  } finally {
    clearTimeout(timer)
  }
}

export async function waitForClosedPosition({
  getPositionState,
  delay,
  attempts,
  pollMs,
}: WaitForCloseOptions): Promise<void> {
  let lastState = ''
  for (let attempt = 0; attempt < attempts; attempt++) {
    lastState = await getPositionState()
    if (lastState === 'closed') return
    if (attempt < attempts - 1) await delay(pollMs)
  }
  if (lastState === 'degraded') {
    throw new Error('A close fill was not confirmed; manual action may be required')
  }
  throw new Error('Close fill confirmation timed out; check the position before retrying')
}
