export interface SingleFlightState {
  running: boolean
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
