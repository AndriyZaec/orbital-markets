/**
 * HACKS / MOCK DATA — TO BE REPLACED WITH REAL API DATA
 *
 * This file contains hardcoded values used for demo/UI purposes.
 * Each item documents what API field should replace it.
 *
 * TODO:
 * - [ ] max_leverage: should come from venue adapter per asset
 * - [ ] apr_24h: needs historical funding rate aggregation (24h window)
 * - [ ] apr_7d: needs historical funding rate aggregation (7d window)
 * - [ ] daily_volume: needs venue market data endpoint
 * - [ ] open_interest: currently using available_notional as proxy
 */

export const DEFAULT_MAX_LEVERAGE = 5

export function getMockLeverage(_asset: string): number {
  return DEFAULT_MAX_LEVERAGE
}

export function getMockApr24h(currentApr: number): number {
  // Simulate 24h APR as slightly different from current
  const seed = Math.abs(currentApr * 1000) % 1
  return currentApr * (0.6 + seed * 0.8)
}

export function getMockApr7d(currentApr: number): number {
  // Simulate 7d APR as more smoothed
  const seed = Math.abs(currentApr * 777) % 1
  return currentApr * (0.3 + seed * 0.6)
}

export function getMockDailyVolume(openInterest: number): number {
  // Simulate daily volume as fraction of OI
  const seed = Math.abs(openInterest * 31) % 1
  return openInterest * (0.05 + seed * 0.3)
}
