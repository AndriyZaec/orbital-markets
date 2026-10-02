import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PositionShareDialog } from '../src/components/PositionShareDialog'
import type { LivePosition } from '../src/hooks/useLivePositions'
import type { LiveFillDetail } from '../src/hooks/useLivePositionDetail'

vi.mock('@/hooks/useLivePositionDetail', () => ({
  useLivePositionDetail: () => ({ data: null, loading: false, error: null, refetch: vi.fn() }),
}))

const position: LivePosition = {
  id: 'position-1', plan_id: 'plan-1', opportunity_id: 'opp-1', asset: 'SOL',
  venue_a: 'pacifica', venue_b: 'hyperliquid', state: 'open', notional: 100,
  leverage: 2, entry_spread: 0, hedge_mismatch: 0, current_spread: 0.1,
  current_basis: 0, entry_basis: 0, basis_change: 0, price_pnl: 1,
  funding_pnl: 1, funding_pnl_source: 'estimated', total_pnl: 2,
  leg1_current_price: 0, leg2_current_price: 0, leg1_liq_price: 0, leg2_liq_price: 0,
  leg1_liq_dist: 0, leg2_liq_dist: 0, leg1_liq_risk: '', leg2_liq_risk: '',
  hold_hours: 1, started_at: '2026-09-30T12:00:00Z', updated_at: '2026-09-30T13:00:00Z',
}

const fills: LiveFillDetail[] = []

const generated = () => {
  const blob = new Blob(['image'], { type: 'image/png' })
  return { blob, file: new File([blob], 'sol-active-position.png', { type: 'image/png' }) }
}

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('PositionShareDialog generation', () => {
  it('shows an accessible loader and disables actions until one generation completes', async () => {
    let finish!: (value: ReturnType<typeof generated>) => void
    const generateCard = vi.fn(() => new Promise<ReturnType<typeof generated>>((resolve) => { finish = resolve }))
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:position-card') })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })

    const { rerender } = render(
      <PositionShareDialog open onOpenChange={() => {}} position={position} fills={fills} kind="active" generateCard={generateCard} />,
    )

    expect(screen.getByText('Generating...')).toBeTruthy()
    expect(screen.getByRole('dialog').getAttribute('aria-busy')).toBe('true')
    expect((screen.getByRole('button', { name: 'Copy image' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Download PNG' }) as HTMLButtonElement).disabled).toBe(true)
    rerender(<PositionShareDialog open onOpenChange={() => {}} position={position} fills={fills} kind="active" generateCard={generateCard} />)
    expect(generateCard).toHaveBeenCalledTimes(1)

    finish(generated())
    await waitFor(() => expect(screen.getByAltText('SOL active position share card')).toBeTruthy())
    expect(screen.getByRole('dialog').getAttribute('aria-busy')).toBe('false')
    expect((screen.getByRole('button', { name: 'Copy image' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('stops loading after a generation error and retries once', async () => {
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:position-card') })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })
    const generateCard = vi.fn()
      .mockRejectedValueOnce(new Error('encode failed'))
      .mockResolvedValueOnce(generated())

    render(<PositionShareDialog open onOpenChange={() => {}} position={position} fills={fills} kind="active" generateCard={generateCard} />)

    expect(await screen.findByText('Unable to generate this position card.')).toBeTruthy()
    expect(screen.getByRole('dialog').getAttribute('aria-busy')).toBe('false')
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    await waitFor(() => expect(generateCard).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByAltText('SOL active position share card')).toBeTruthy())
  })

  it('can be closed while generation is still pending', async () => {
    const onOpenChange = vi.fn()
    const generateCard = vi.fn(() => new Promise<ReturnType<typeof generated>>(() => {}))

    render(<PositionShareDialog open onOpenChange={onOpenChange} position={position} fills={fills} kind="active" generateCard={generateCard} />)

    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(onOpenChange.mock.calls[0]?.[0]).toBe(false)
  })

  it('stops loading when position details never arrive', async () => {
    vi.useFakeTimers()
    render(<PositionShareDialog open onOpenChange={() => {}} position={position} kind="active" />)

    await act(async () => { vi.advanceTimersByTime(10_000) })

    expect(screen.getByText('Unable to load position details for this card.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(screen.getByText('Generating...')).toBeTruthy()
    await act(async () => { vi.advanceTimersByTime(10_000) })
    expect(screen.getByText('Unable to load position details for this card.')).toBeTruthy()
  })
})
