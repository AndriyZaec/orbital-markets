import assert from 'node:assert/strict'
import test from 'node:test'
import {
  canRequestLiveClose,
  hasActionableRecordedFills,
  recoveryPresentation,
} from '../src/lib/degraded-execution.ts'

test('degraded execution always routes the user to exposure review', () => {
  const withExposure = recoveryPresentation('degraded', 'submit_failed', 1)
  assert.equal(withExposure.action, 'view_positions')
  assert.equal(withExposure.actionLabel, 'Review & Close Exposure')

  const uncertain = recoveryPresentation('degraded', 'unconfirmed', 0)
  assert.equal(uncertain.action, 'view_positions')
  assert.equal(uncertain.actionLabel, 'Review Positions')
})

test('retry is allowed only after a clean abort or a pre-exposure failure', () => {
  assert.equal(recoveryPresentation('aborted', 'confirmed', 0).action, 'retry')
  assert.equal(recoveryPresentation('aborted', 'unconfirmed', 0).action, 'view_positions')
  assert.equal(recoveryPresentation('aborted', 'confirmed', 1).action, 'view_positions')
  assert.equal(recoveryPresentation('failed', null, 0).action, 'retry')
})

test('close actions require a confirmed positive recorded fill', () => {
  assert.equal(hasActionableRecordedFills([]), false)
  assert.equal(hasActionableRecordedFills([{ filled: false, filled_amount: 10 }]), false)
  assert.equal(hasActionableRecordedFills([{ filled: true, filled_amount: 0 }]), false)
  assert.equal(hasActionableRecordedFills([{ filled: true, filled_amount: 2.75 }]), true)
})

test('an open position remains closeable before detail fills render', () => {
  assert.equal(canRequestLiveClose('open', []), true)
  assert.equal(canRequestLiveClose('degraded', []), false)
  assert.equal(canRequestLiveClose('closing', [{ filled: true, filled_amount: 2.75 }]), true)
})
