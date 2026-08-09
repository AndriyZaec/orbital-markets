export interface SingleFlightState {
  running: boolean
}

export function livePositionPollInterval(
  hasActivePosition: boolean | null,
  activeInterval = 5_000,
): number {
  return hasActivePosition === false ? 30_000 : activeInterval
}

export function shouldMonitorLiveUpdates(
  pageVisible: boolean,
  hasActivePosition: boolean | null,
  executionActive = false,
): boolean {
  return pageVisible || executionActive || hasActivePosition !== false
}

export async function runSingleFlight(
  state: SingleFlightState,
  task: () => Promise<void>,
): Promise<void> {
  if (state.running) return
  state.running = true
  try {
    await task()
  } finally {
    state.running = false
  }
}
