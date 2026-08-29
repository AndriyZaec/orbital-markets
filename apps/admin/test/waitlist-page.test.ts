import { describe, expect, it } from 'vitest'
import { canDisableInvite } from '../src/features/WaitlistPage'

describe('invite actions', () => {
  it('keeps the disable action available after redemption', () => {
    expect(canDisableInvite('redeemed')).toBe(true)
  })
})
