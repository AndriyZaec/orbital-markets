// @vitest-environment jsdom

import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { DeferredBoundary } from '../src/components/DeferredBoundary'

function BrokenChunk(): never {
  throw new Error('chunk unavailable')
}

describe('DeferredBoundary', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows a recoverable state when deferred UI fails to load', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)

    render(
      <DeferredBoundary label="Account management">
        <BrokenChunk />
      </DeferredBoundary>,
    )

    expect(screen.getByText('Account management could not be loaded.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Reload' })).toBeTruthy()
  })
})
