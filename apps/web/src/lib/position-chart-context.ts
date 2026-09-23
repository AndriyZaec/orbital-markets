import type { Venue } from '@/agents/types'
import type { FundingDirection } from '@/lib/funding-chart'

export type LivePositionChartContext = {
  available: false
  unavailable_reason: string
} | AvailablePositionChartContext

export interface AvailablePositionChartContext {
  available: true
  asset: string
  venue_a: Venue
  venue_b: Venue
  direction: FundingDirection
  current_apr: number
  notional: number
  fee_estimate: number
  slippage_estimate: number
  projection_source: 'execution_plan' | 'fills_fallback'
  projection_warning?: string
}
