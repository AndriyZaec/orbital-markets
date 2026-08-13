export type RecoveryPhase = 'open' | 'recovering' | 'degraded' | 'aborted' | 'failed'
export type RecoveryAction = 'view_positions' | 'retry'
export type RecoveryTone = 'green' | 'blue' | 'orange' | 'red'

export interface RecoveryPresentation {
  title: string
  description: string
  action: RecoveryAction
  actionLabel: string
  tone: RecoveryTone
}

export function recoveryPresentation(
  phase: RecoveryPhase,
  unwindStatus: string | null,
  exposureCount: number,
): RecoveryPresentation {
  if (phase === 'open') {
    return {
      title: 'Hedge opened',
      description: 'Both legs filled within the hedge tolerance.',
      action: 'view_positions',
      actionLabel: 'View Position',
      tone: 'green',
    }
  }
  if (phase === 'recovering') {
    return {
      title: 'Checking venue positions',
      description: 'The submission result was uncertain. Orbital is reconciling venue state before recommending an action.',
      action: 'view_positions',
      actionLabel: 'Monitor Positions',
      tone: 'blue',
    }
  }
  if (phase === 'degraded') {
    return exposureCount > 0
      ? {
          title: 'Residual exposure detected',
          description: 'One or more legs may remain open. Review the reported exposure and close it before starting another trade.',
          action: 'view_positions',
          actionLabel: 'Review & Close Exposure',
          tone: 'orange',
        }
      : {
          title: 'Exposure status needs review',
          description: 'The hedge did not complete cleanly, but no exact residual amount was reported. Verify the position before retrying.',
          action: 'view_positions',
          actionLabel: 'Review Positions',
          tone: 'orange',
        }
  }
  if (phase === 'aborted' && unwindStatus === 'confirmed' && exposureCount === 0) {
    return {
      title: 'Open safely aborted',
      description: 'The filled leg was closed and no residual exposure was reported.',
      action: 'retry',
      actionLabel: 'Try Again',
      tone: 'orange',
    }
  }
  if (phase === 'aborted') {
    return {
      title: 'Open aborted with uncertain exposure',
      description: 'Review venue positions before attempting another trade.',
      action: 'view_positions',
      actionLabel: 'Review Positions',
      tone: 'orange',
    }
  }
  return {
    title: 'Execution failed',
    description: 'No order was confirmed open. You can correct the issue and retry.',
    action: 'retry',
    actionLabel: 'Try Again',
    tone: 'red',
  }
}

export function hasActionableRecordedFills(
  fills: Array<{ filled: boolean; filled_amount: number }>,
): boolean {
  return fills.some((fill) => fill.filled && fill.filled_amount > 0)
}

export function canRequestLiveClose(
  state: string,
  fills: Array<{ filled: boolean; filled_amount: number }>,
): boolean {
  if (state === 'open') return true
  return (state === 'degraded' || state === 'closing') && hasActionableRecordedFills(fills)
}
