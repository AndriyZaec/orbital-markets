export interface WaitForCloseOptions {
  getPositionState: () => Promise<string>
  delay: (ms: number) => Promise<void>
  attempts: number
  pollMs: number
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
