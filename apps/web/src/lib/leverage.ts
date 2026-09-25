export interface LeverageSelection {
  opportunityId: string
  value: number
}

interface CapabilityLike {
  status: 'known' | 'pending' | 'stale' | 'missing' | 'unsupported' | 'out_of_range'
  reason?: string
}

export function leverageCapabilityMessage(capabilities?: Record<string, CapabilityLike>): string | null {
  if (!capabilities) return null
  for (const [venue, capability] of Object.entries(capabilities)) {
    if (capability.status === 'known' || capability.status === 'unsupported') continue
    const label = venue === 'aster' ? 'Aster' : venue.charAt(0).toUpperCase() + venue.slice(1)
    if (capability.reason === 'target_refresh_failed') return `${label} leverage refresh failed. Try again.`
    switch (capability.status) {
      case 'pending': return capability.reason === 'account_pending' ? `${label} account leverage is loading` : `${label} leverage is loading`
      case 'stale': return capability.reason === 'account_unavailable' ? `${label} account data is stale` : `${label} leverage data is stale`
      case 'missing': return `${label} has no bracket for this market`
      case 'out_of_range': return capability.reason === 'invalid_notional' ? 'Enter a valid position size' : `Position size is outside ${label} leverage brackets`
    }
  }
  return null
}

export function knownMaxLeverage(value: number | null | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null
}

export function reconcileLeverageSelection(
  current: LeverageSelection,
  opportunityId: string,
  publicMaximum: number | null,
  authoritativeMaximum: number | null,
): LeverageSelection {
  if (authoritativeMaximum === null) return current
  if (current.opportunityId !== opportunityId) {
    return { opportunityId, value: authoritativeMaximum }
  }
  const value = publicMaximum === null && current.value === 1
    ? authoritativeMaximum
    : Math.min(current.value, authoritativeMaximum)
  return value === current.value ? current : { ...current, value }
}
