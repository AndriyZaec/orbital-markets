import assert from 'node:assert/strict'
import test from 'node:test'

import { closedPositionShareMetrics } from '../src/lib/closed-position-share.ts'

const profitable = {
  asset: 'SOL',
  notional: 100,
  leverage: 2,
  total_pnl: 10,
  hold_hours: 24,
  started_at: '2026-09-29T12:00:00Z',
  opened_at: '2026-09-29T12:00:00Z',
  completed_at: '2026-09-30T12:00:00Z',
}

test('profitable closed cards lead with APR and retain actual ROI', () => {
  assert.deepEqual(closedPositionShareMetrics(profitable), {
    heroLabel: 'APR',
    heroValue: 36.5,
    roi: 0.1,
    holdDuration: '1d',
  })
})

test('losing closed cards lead with negative ROI and never annualize the loss', () => {
  assert.deepEqual(closedPositionShareMetrics({ ...profitable, total_pnl: -10 }), {
    heroLabel: 'ROI',
    heroValue: -0.1,
    roi: -0.1,
    holdDuration: '1d',
  })
})
