export const hoursPerYear = 8760

export type FundingDirection = 'long_a_short_b' | 'long_b_short_a'

export interface FundingSample {
  t: string
  funding_a: number
  funding_b: number
}

export interface FundingStats {
  currentCarry: number
  averageCarry: number
  positiveShare: number
}

export interface ReturnProjection {
  costs: number
  baseCarryPerHour: number
  lowerCarryPerHour: number
  upperCarryPerHour: number
  potentialReturn: number
  breakEvenHours: number | null
}

export interface ChartDomain {
  min: number
  max: number
}

export function annualizeFunding(ratePerHour: number) {
  return ratePerHour * hoursPerYear
}

export function directionalCarry(sample: FundingSample, direction: FundingDirection) {
  return direction === 'long_a_short_b'
    ? sample.funding_b - sample.funding_a
    : sample.funding_a - sample.funding_b
}

export function calculateFundingStats(samples: FundingSample[], direction: FundingDirection): FundingStats | null {
  if (samples.length === 0) return null

  const carries = samples.map((sample) => directionalCarry(sample, direction))
  const averageCarry = carries.reduce((sum, carry) => sum + carry, 0) / carries.length

  return {
    currentCarry: annualizeFunding(carries[carries.length - 1]),
    averageCarry: annualizeFunding(averageCarry),
    positiveShare: carries.filter((carry) => carry > 0).length / carries.length,
  }
}

export function calculateReturnProjection(
  samples: FundingSample[],
  direction: FundingDirection,
  notional: number,
  costRate: number,
  horizonHours: number,
): ReturnProjection | null {
  if (samples.length === 0 || notional <= 0 || horizonHours <= 0) return null

  const carries = samples.map((sample) => directionalCarry(sample, direction)).sort((a, b) => a - b)
  const baseCarryPerHour = carries.reduce((sum, carry) => sum + carry, 0) / carries.length
  const lowerCarryPerHour = percentile(carries, 0.25)
  const upperCarryPerHour = percentile(carries, 0.75)
  const costs = notional * Math.max(0, costRate)
  const hourlyReturn = notional * baseCarryPerHour

  return {
    costs,
    baseCarryPerHour,
    lowerCarryPerHour,
    upperCarryPerHour,
    potentialReturn: hourlyReturn * horizonHours - costs,
    breakEvenHours: hourlyReturn > 0 ? costs / hourlyReturn : null,
  }
}

export function paddedChartDomain(values: number[], minimumSpan: number, paddingRatio = 0.12): ChartDomain {
  const finiteValues = values.filter(Number.isFinite)
  if (finiteValues.length === 0) return { min: -minimumSpan / 2, max: minimumSpan / 2 }

  const valueMin = Math.min(...finiteValues)
  const valueMax = Math.max(...finiteValues)
  const valueSpan = valueMax - valueMin
  const span = Math.max(valueSpan, minimumSpan)
  const center = (valueMin + valueMax) / 2
  const domainMin = valueSpan < minimumSpan ? center - span / 2 : valueMin
  const domainMax = valueSpan < minimumSpan ? center + span / 2 : valueMax
  const padding = span * paddingRatio

  return { min: domainMin - padding, max: domainMax + padding }
}

function percentile(sortedValues: number[], quantile: number) {
  if (sortedValues.length === 1) return sortedValues[0]

  const index = (sortedValues.length - 1) * quantile
  const lower = Math.floor(index)
  const remainder = index - lower
  const upper = sortedValues[lower + 1]

  return upper === undefined
    ? sortedValues[lower]
    : sortedValues[lower] + remainder * (upper - sortedValues[lower])
}
