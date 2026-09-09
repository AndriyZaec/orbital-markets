export interface LeverageSelection {
  opportunityId: string
  value: number
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
