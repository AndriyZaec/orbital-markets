import test from 'node:test'
import assert from 'node:assert/strict'
import { validatedTelegramURL } from '../src/lib/telegram.ts'

test('accepts only HTTPS Telegram deep links', () => {
  assert.equal(
    validatedTelegramURL('https://t.me/orbital_bot?start=token'),
    'https://t.me/orbital_bot?start=token',
  )
  assert.throws(() => validatedTelegramURL('https://example.com/phishing'))
  assert.throws(() => validatedTelegramURL('http://t.me/orbital_bot'))
  assert.throws(() => validatedTelegramURL(undefined))
})
