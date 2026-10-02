// @vitest-environment jsdom

import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { DeferredSurface } from '../src/App'

describe('DeferredSurface', () => {
  it('uses the Orbital loader and preserves a deferred side panel width', () => {
    render(
      <DeferredSurface
        label="Loading execution panel"
        className="w-[340px] shrink-0"
      />,
    )

    const loader = screen.getByRole('status', { name: 'Loading execution panel' })
    expect(loader.classList.contains('w-[340px]')).toBe(true)
    expect(loader.classList.contains('shrink-0')).toBe(true)
    expect(loader.querySelector('.orbital-logo')).not.toBeNull()
    expect(screen.queryByText('Loading execution panel')).toBeNull()
  })
})
