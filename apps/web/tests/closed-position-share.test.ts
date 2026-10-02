import assert from 'node:assert/strict'
import test from 'node:test'

import { positionShareMetrics } from '../src/lib/position-share.ts'

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
  assert.deepEqual(positionShareMetrics(profitable, 'closed'), {
    title: 'Closed Position',
    heroLabel: 'APR',
    heroValue: 36.5,
    roi: 0.1,
    holdDuration: '1d',
    durationLabel: 'HOLD',
    pnlLabel: 'REALIZED PNL',
    pnlValue: 10,
    deployedCapital: 100,
  })
})

test('losing closed cards lead with negative ROI and never annualize the loss', () => {
  assert.deepEqual(positionShareMetrics({ ...profitable, total_pnl: -10 }, 'closed'), {
    title: 'Closed Position',
    heroLabel: 'ROI',
    heroValue: -0.1,
    roi: -0.1,
    holdDuration: '1d',
    durationLabel: 'HOLD',
    pnlLabel: 'REALIZED PNL',
    pnlValue: -10,
    deployedCapital: 100,
  })
})

test('active cards use current unrealized semantics and an active duration', () => {
  assert.deepEqual(positionShareMetrics({ ...profitable, completed_at: undefined }, 'active', Date.parse('2026-09-30T00:00:00Z')), {
    title: 'Active Position',
    heroLabel: 'CURRENT APR',
    heroValue: 73,
    roi: 0.1,
    holdDuration: '12h',
    durationLabel: 'ACTIVE FOR',
    pnlLabel: 'UNREALIZED PNL',
    pnlValue: 10,
    deployedCapital: 100,
  })
})

test('closed cards retain recorded hold time when completion time is absent', () => {
  const metrics = positionShareMetrics({ ...profitable, completed_at: undefined }, 'closed', Date.parse('2027-09-30T12:00:00Z'))
  assert.equal(metrics?.holdDuration, '1d')
  assert.equal(metrics?.heroValue, 36.5)
})
