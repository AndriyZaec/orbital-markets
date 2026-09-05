import { waitForClosedPosition } from './live-close.ts'

export interface KillPreparationPosition {
  id: string
  legs_to_close: number
  error?: string
}

export interface WaitForKilledPositionsOptions {
  positionIds: string[]
  getPositionState: (positionId: string) => Promise<string>
  delay: (ms: number) => Promise<void>
  attempts: number
  pollMs: number
}

export async function waitForKilledPositions({
  positionIds,
  getPositionState,
  delay,
  attempts,
  pollMs,
}: WaitForKilledPositionsOptions): Promise<void> {
  await Promise.all(positionIds.map(positionId => waitForClosedPosition({
    getPositionState: () => getPositionState(positionId),
    delay,
    attempts,
    pollMs,
  })))
}

export function summarizeKillPreparation(
  targeted: number,
  requestCount: number,
  positions: KillPreparationPosition[],
): { failed: number; errors: string[] } {
  const failedPositions = positions.filter(position => position.error)
  const errors = failedPositions.map(position => `${position.id}: ${position.error}`)
  const failed = failedPositions.reduce((total, position) => total + Math.max(position.legs_to_close, 1), 0)

  if (targeted > 0 && requestCount === 0 && errors.length === 0) {
    return {
      failed: targeted,
      errors: ['No emergency close orders were prepared for the targeted positions'],
    }
  }

  return { failed, errors }
}
