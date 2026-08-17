import test from 'node:test'
import assert from 'node:assert/strict'
import { apiError, userErrorMessage } from '../src/lib/api.ts'

test('maps raw HTTP statuses to user-facing messages', () => {
  assert.equal(apiError(404, 'Telegram connection is not available yet.').message, 'Telegram connection is not available yet.')
  assert.equal(apiError(429, 'fallback').message, 'Too many requests. Wait a moment and try again.')
  assert.equal(apiError(503, 'fallback').message, 'Orbital is temporarily unavailable. Please try again shortly.')
})

test('preserves actionable backend domain errors', () => {
  assert.equal(
    apiError(422, 'Unable to prepare execution.', { error: 'Insufficient available margin' }).message,
    'Insufficient available margin',
  )
})

test('does not expose backend details for infrastructure and authentication failures', () => {
  assert.equal(
    apiError(503, 'fallback', { error: 'database is locked at /data/orbital.db' }).message,
    'Orbital is temporarily unavailable. Please try again shortly.',
  )
  assert.equal(
    apiError(401, 'fallback', { error: 'internal authorization detail' }).message,
    'Your session has expired. Refresh the page and try again.',
  )
})

test('preserves domain details for forbidden trading actions', () => {
  assert.equal(
    apiError(403, 'Unable to prepare execution.', { error: 'Live admission denied' }).message,
    'Live admission denied',
  )
})

test('maps network failures without exposing implementation details', () => {
  assert.equal(
    userErrorMessage(new TypeError('Failed to fetch'), 'Fallback'),
    'Unable to reach Orbital. Check your connection and try again.',
  )
  assert.equal(userErrorMessage(new TypeError('Invalid URL'), 'Fallback'), 'Invalid URL')
})
